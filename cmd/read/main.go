package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ehrlich-b/read/internal/embedding"
	"github.com/ehrlich-b/read/internal/relay"
)

//go:embed feeds.md
var feedsMD string

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "post":
			postCmd(os.Args[2:])
			return
		case "seed":
			seedCmd(os.Args[2:])
			return
		case "fetch":
			fetchCmd(os.Args[2:])
			return
		case "process":
			processCmd(os.Args[2:])
			return
		case "reassign":
			reassignCmd(os.Args[2:])
			return
		}
	}
	serveCmd()
}

func serveCmd() {
	port := flag.Int("port", 8080, "HTTP port")
	dbPath := flag.String("db", defaultDBPath(), "SQLite database path")
	flag.Parse()

	store, err := relay.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := relay.NewServer(store)

	// Load feed sources for about page (embedded at build time)
	srv.Feeds = relay.ParseFeeds(feedsMD)

	// Set up embedder for post API (optional — server works without it)
	emb, err := embedding.NewFromProvider("auto", "", "")
	if err != nil {
		log.Printf("warning: no embedder available, POST /api/post disabled (%v)", err)
	} else {
		srv.Embedder = emb
		log.Printf("embedder: %s", emb.Name())
	}

	log.Printf("listening on :%d (db: %s)", *port, *dbPath)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), srv))
}

func postCmd(args []string) {
	fs := flag.NewFlagSet("post", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path")
	title := fs.String("title", "", "Article title")
	link := fs.String("link", "", "Article URL")
	mass := fs.Int("mass", 10, "Quality score (1-10000)")
	date := fs.String("date", "", "Published date (RFC3339 or YYYY-MM-DD)")
	fs.Parse(args)

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: read post [flags] <text>")
		os.Exit(1)
	}
	text := fs.Arg(0)

	store, err := relay.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	emb, err := embedding.NewFromProvider("auto", "", "")
	if err != nil {
		log.Fatalf("embedder: %v", err)
	}

	params := relay.PostParams{
		UserID: "pipeline",
		Text:   text,
		Title:  *title,
		Link:   *link,
		Mass:   *mass,
	}
	if *date != "" {
		t, err := time.Parse(time.RFC3339, *date)
		if err != nil {
			t, err = time.Parse("2006-01-02", *date)
		}
		if err != nil {
			log.Fatalf("parse date: %v", err)
		}
		params.PublishedAt = &t
	}

	post, err := relay.CreatePost(store, emb, params)
	if err != nil {
		log.Fatalf("create post: %v", err)
	}
	fmt.Printf("posted: %s\n", post.ID)
}

func seedCmd(args []string) {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path")
	spacesPath := fs.String("spaces", "spaces.yaml", "Path to spaces.yaml")
	cacheDir := fs.String("cache", "spaces/cache", "Embedding cache directory")
	fs.Parse(args)

	store, err := relay.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	emb, err := embedding.NewFromProvider("auto", "", "")
	if err != nil {
		log.Fatalf("embedder: %v", err)
	}

	idx, err := embedding.LoadSpaceIndex(*spacesPath, *cacheDir, emb)
	if err != nil {
		log.Fatalf("load space index: %v", err)
	}

	n, err := relay.SeedSpacesFromIndex(store, idx, emb.Name())
	if err != nil {
		log.Fatalf("seed spaces: %v", err)
	}
	fmt.Printf("seeded %d spaces\n", n)
}

func reassignCmd(args []string) {
	fs := flag.NewFlagSet("reassign", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path")
	fs.Parse(args)

	store, err := relay.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	emb, err := embedding.NewFromProvider("auto", "", "")
	if err != nil {
		log.Fatalf("embedder: %v", err)
	}

	// Get current valid anchor IDs
	anchorEmbs, err := store.ListAnchorsForEmbedder(emb.Name())
	if err != nil || len(anchorEmbs) == 0 {
		log.Fatalf("no anchor embeddings found for %s", emb.Name())
	}
	validIDs := make(map[string]bool)
	for _, ae := range anchorEmbs {
		validIDs[ae.AnchorID] = true
	}

	// Find post_anchors pointing to dead anchors (invisible or missing)
	rows, err := store.DB().Query(
		`SELECT pa.post_id, pa.anchor_id, p.embedding_512
		 FROM post_anchors pa
		 JOIN social_embeddings p ON p.id = pa.post_id
		 LEFT JOIN social_embeddings a ON a.id = pa.anchor_id AND a.kind = 'anchor' AND a.visible = 1
		 WHERE a.id IS NULL`)
	if err != nil {
		log.Fatalf("query dead anchors: %v", err)
	}

	type deadAssignment struct {
		postID   string
		anchorID string
		vecBytes []byte
	}
	var dead []deadAssignment
	for rows.Next() {
		var d deadAssignment
		if err := rows.Scan(&d.postID, &d.anchorID, &d.vecBytes); err != nil {
			log.Fatalf("scan: %v", err)
		}
		dead = append(dead, d)
	}
	rows.Close()

	if len(dead) == 0 {
		fmt.Println("no orphaned assignments found")
		return
	}

	// For each dead assignment, find the best matching current anchor
	updated := 0
	for _, d := range dead {
		if len(d.vecBytes) == 0 {
			continue
		}
		postVec := embedding.BytesAsVec(d.vecBytes)

		bestID := ""
		bestSim := float32(0)
		for _, ae := range anchorEmbs {
			if len(ae.Centroid512) == 0 {
				continue
			}
			anchorVec := embedding.BytesAsVec(ae.Centroid512)
			sim := embedding.Cosine(postVec, anchorVec)
			if sim > bestSim {
				bestSim = sim
				bestID = ae.AnchorID
			}
		}

		if bestSim < 0.40 {
			// Below threshold, just delete the assignment
			store.DB().Exec("DELETE FROM post_anchors WHERE post_id = ? AND anchor_id = ?", d.postID, d.anchorID)
		} else {
			// Check if post already has this anchor (from other slot)
			var count int
			store.DB().QueryRow("SELECT COUNT(*) FROM post_anchors WHERE post_id = ? AND anchor_id = ?", d.postID, bestID).Scan(&count)
			if count > 0 {
				// Already assigned to this anchor in other slot, just remove the dead one
				store.DB().Exec("DELETE FROM post_anchors WHERE post_id = ? AND anchor_id = ?", d.postID, d.anchorID)
			} else {
				store.DB().Exec("UPDATE post_anchors SET anchor_id = ?, similarity = ? WHERE post_id = ? AND anchor_id = ?",
					bestID, bestSim, d.postID, d.anchorID)
				updated++
			}
		}
	}
	fmt.Printf("reassigned %d post-anchor slots (%d orphans total)\n", updated, len(dead))
}

func defaultDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".read", "read.db")
}
