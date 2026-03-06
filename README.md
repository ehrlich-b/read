# read.ehrlich.dev

AI-curated RSS reader. Fetches articles from ~500 RSS feeds, compresses and scores them with Claude, embeds them for semantic similarity, and serves a ranked feed across topic spaces.

**Live at [read.ehrlich.dev](http://read.ehrlich.dev)**

## How it works

1. **Fetch** -- `scripts/pipeline.go` pulls articles from RSS feeds listed in `skills/feeds.md`
2. **Compress & Score** -- `scripts/compress_and_post.sh` uses Claude to compress each article to ~800 chars and estimate an HN-style upvote score (1-10000)
3. **Embed & Assign** -- Posts are embedded (OpenAI text-embedding-3-small or local Ollama) and assigned to the top-2 matching topic spaces by cosine similarity
4. **Serve** -- Go HTTP server with SQLite, dark-mode UI, hot/new/week/month/year sorts

## Build & Run

```
go build -o read ./cmd/read
./read --port 8080 --db ~/.read/read.db
```

Requires an embedder for ingesting posts (not for serving):
- **Ollama** (local): Install ollama, pull `mxbai-embed-large`
- **OpenAI**: Set `OPENAI_API_KEY`

## Ingest pipeline

```bash
# Fetch RSS feeds into TSV
go run scripts/pipeline.go skills/feeds.md > /tmp/articles.tsv

# Compress, score, and post each article
scripts/compress_and_post.sh ./read /tmp/articles.tsv
```

## Seed topic spaces

Spaces are defined in `spaces.yaml` with keyword centroids. Seed them into the database:

```
./read seed --spaces spaces.yaml
```

## Deploy

```bash
export READ_DEPLOY_HOST=root@your-server-ip
scripts/deploy.sh
```

## Architecture

```
cmd/read/           CLI: serve, post, seed
internal/relay/     SQLite store, HTTP server, HTML templates
internal/embedding/ Embedder interface, OpenAI + Ollama adapters, space index
scripts/            RSS pipeline, deployment, maintenance tools
skills/             Claude prompt templates (compression, scoring)
spaces.yaml         Topic space definitions with keyword centroids
```

## Key concepts

- **Spaces** -- Semantic topics (golang, astrophysics, etc.) defined by keyword centroids
- **Mass** -- AI-estimated quality score calibrated to HN upvote scale
- **Decay** -- 12-hour half-life exponential decay for "hot" sort
- **Swallowed** -- Posts below 0.25 cosine similarity to all spaces are hidden

## License

MIT
