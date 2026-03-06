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
	if len(os.Args) > 1 && os.Args[1] == "post" {
		postCmd(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "seed" {
		seedCmd(os.Args[2:])
		return
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

func defaultDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".read", "read.db")
}
