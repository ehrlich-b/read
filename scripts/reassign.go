//go:build ignore

// Reassign all posts to anchors using current embedder centroids.
// Usage: go run scripts/reassign.go
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/ehrlich-b/read/internal/embedding"
	"github.com/ehrlich-b/read/internal/relay"
)

func main() {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".read", "read.db")

	store, err := relay.Open(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	emb, err := embedding.NewFromProvider("auto", "", "")
	if err != nil {
		log.Fatalf("embedder: %v", err)
	}

	anchorEmbs, err := store.ListAnchorsForEmbedder(emb.Name())
	if err != nil {
		log.Fatalf("list anchors: %v", err)
	}
	fmt.Printf("loaded %d anchor centroids for %s\n", len(anchorEmbs), emb.Name())

	// Get all posts
	rows, err := store.DB().Query(
		`SELECT id, embedding_512 FROM social_embeddings WHERE kind = 'post' AND embedding_512 IS NOT NULL`)
	if err != nil {
		log.Fatalf("query posts: %v", err)
	}

	type post struct {
		id  string
		emb []byte
	}
	var posts []post
	for rows.Next() {
		var p post
		if err := rows.Scan(&p.id, &p.emb); err != nil {
			log.Fatalf("scan: %v", err)
		}
		posts = append(posts, p)
	}
	rows.Close()
	fmt.Printf("reassigning %d posts\n", len(posts))

	type anchorMatch struct {
		ID         string
		Similarity float32
	}

	reassigned := 0
	for _, p := range posts {
		if len(p.emb) == 0 {
			continue
		}
		vec := embedding.BytesAsVec(p.emb)

		var matches []anchorMatch
		for _, ae := range anchorEmbs {
			if len(ae.Centroid512) == 0 {
				continue
			}
			anchorVec := embedding.BytesAsVec(ae.Centroid512)
			sim := embedding.Cosine(vec, anchorVec)
			matches = append(matches, anchorMatch{ID: ae.AnchorID, Similarity: sim})
		}

		sort.Slice(matches, func(i, j int) bool {
			return matches[i].Similarity > matches[j].Similarity
		})

		// Delete old assignments
		store.DB().Exec("DELETE FROM post_anchors WHERE post_id = ?", p.id)

		// Assign top 2 above 0.40
		var assignments []relay.PostAnchor
		for j, m := range matches {
			if j >= 2 || m.Similarity < 0.40 {
				break
			}
			assignments = append(assignments, relay.PostAnchor{
				PostID:     p.id,
				AnchorID:   m.ID,
				Similarity: float64(m.Similarity),
			})
		}

		if len(assignments) > 0 {
			store.AssignPostAnchors(p.id, assignments)
		}

		// Update swallowed status
		swallowed := len(matches) == 0 || matches[0].Similarity < 0.25
		visible := 1
		if swallowed {
			visible = 0
		}
		sw := 0
		if swallowed {
			sw = 1
		}
		store.DB().Exec("UPDATE social_embeddings SET visible = ?, swallowed = ? WHERE id = ?", visible, sw, p.id)

		reassigned++
		if reassigned%500 == 0 {
			fmt.Printf("  %d/%d\n", reassigned, len(posts))
		}
	}

	fmt.Printf("done: reassigned %d posts across %d anchors\n", reassigned, len(anchorEmbs))
}
