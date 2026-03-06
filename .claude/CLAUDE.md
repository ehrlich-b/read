# read.ehrlich.dev

AI-curated RSS reader. Fetches technical feeds, compresses articles with Claude, scores them (AI-generated "upvotes"), assigns to semantic topic spaces via embedding similarity, and serves a ranked feed.

## Architecture

- `cmd/read/` — CLI: `read` (serve), `read post` (ingest article), `read seed` (seed spaces)
- `internal/relay/` — Data layer (SQLite), HTTP server, templates
- `internal/embedding/` — Embedder interface, ollama/openai adapters, space index, cosine similarity
- `spaces.yaml` — 159 semantic topic spaces with keyword centroids
- `skills/` — Claude prompt templates for compression and scoring
- `scripts/` — RSS pipeline tools

## Build & Run

```
go build -o read ./cmd/read
./read --port 8080 --db ~/.read/read.db
```

## Database

SQLite at `~/.read/read.db`. Migrations in `internal/relay/migrations/`.

## Pipeline

```
go run scripts/pipeline.go skills/feeds.md > /tmp/articles.tsv
scripts/compress_and_post.sh ./read /tmp/articles.tsv
```

## Key Concepts

- **Spaces**: Semantic topics (golang, astrophysics, etc.) defined by keyword centroids in spaces.yaml
- **Anchors**: Space centroids embedded as vectors; posts are assigned to top-2 matching anchors by cosine similarity (threshold 0.40)
- **Mass**: AI-estimated quality score (1-10000), calibrated to HN upvote scale
- **Decay**: 12-hour half-life exponential decay on mass, computed at query time for "hot" sort
- **Swallowed**: Posts below 0.25 similarity to all anchors are hidden (spam filter)

## Module

`github.com/ehrlich-b/read`
