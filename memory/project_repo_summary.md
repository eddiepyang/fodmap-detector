---
name: repo_summary
description: Overview of the fodmap-detector repo — stack, architecture, endpoints, and key design decisions
type: project
---

## What this project is

A Go HTTP server + CLI that analyzes Yelp restaurant reviews for FODMAP content. Users search for restaurants by natural language, then trigger LLM-based analysis of a specific business's reviews to flag FODMAP ingredients.

## Stack

- **Language**: Go 1.24
- **HTTP**: stdlib `net/http` with Go 1.22+ pattern routing (`http.NewServeMux`)
- **LLM**: Google Gemini 2.0 Flash (`GEMINI_API_KEY` env var required)
- **Vector DB**: Weaviate with `text2vec-transformers` (local embeddings, runs via Docker)
- **CLI**: Cobra (`go run ./cmd/cli`)
- **Data formats**: Parquet (batch), Avro OCF (streaming), JSONL input

## Key files

| File | Purpose |
|------|---------|
| `server/server.go` | Route registration |
| `server/handlers.go` | HTTP handlers |
| `server/jobs.go` | In-memory async job store |
| `server/llm.go` | Gemini client with rate limiting |
| `search/weaviate.go` | Weaviate client: schema, batch upsert, nearText search |
| `data/data.go` | Archive reading, data transforms |
| `cli/` | Cobra subcommands: serve, index, batch, event |
| `docs/search.md` | Search API reference and design decisions |

## HTTP API

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/analyze?business_id=` | Start async FODMAP analysis; returns `{"job_id":"..."}` |
| `GET` | `/results/{job_id}` | Poll job status/result |
| `GET` | `/reviews?business_id=` | List raw reviews for a business from the archive |
| `GET` | `/search/{query...}` | Semantic restaurant search via Weaviate |

### Search query params (all optional)
- `limit` — number of results, default 10
- `category` — substring filter on business categories
- `city` — exact city match
- `state` — exact state match

Example: `curl "localhost:8080/search/cozy Italian with great pasta?city=Philadelphia&limit=5"`

The query is a **path segment** (not `?q=`), supports spaces and slashes via `{query...}` wildcard.

## Search ranking

Top-K average similarity (K=5): average of the top 5 most relevant reviews per business. Avoids volume bias (popular chains don't dominate) and outlier noise.

## Data flow

```
data/archive.tar.gz  (Yelp JSONL, gzip)
        |
   GetArchive("review") / GetArchive("business")
        |
   [index cmd] → Weaviate (YelpReview collection)
   [serve cmd] → HTTP server
        |
   /search/{query} → nearText query → ranked business IDs
   /analyze        → Gemini LLM     → FODMAP analysis result
```

## Running locally

```sh
docker compose up -d                          # Start Weaviate on :8090
go run ./cmd/cli index --weaviate localhost:8090  # Index reviews
go run . serve --weaviate localhost:8090          # Start server on :8080
```
