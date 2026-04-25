# FODMAP Detector

A Go CLI tool that processes Yelp dataset reviews to identify FODMAP (Fermentable Oligosaccharides, Disaccharides, Monosaccharides, and Polyols) content in food items. Designed to help people with digestive sensitivities by analyzing restaurant reviews for dish ingredients and flagging FODMAP groups.

---

## Purpose

1. Read Yelp review data from a compressed archive (`.tar.gz` of JSON lines)
2. Serialize reviews to Apache Avro (streaming) format
3. Provide an interactive semantic search and chat agent for FODMAP/allergen queries

---

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.26+ |
| CLI | [Cobra](https://github.com/spf13/cobra) |
| Streaming format | Apache Avro (OCF) via [hamba/avro](https://github.com/hamba/avro) |
| Input | TAR.GZ compressed JSON lines (Yelp dataset) |
| Concurrency | Go channels + goroutines |
| Vector search | [Weaviate](https://weaviate.io) (Local) or [Pinecone](https://pinecone.io) (Cloud) |
| Embeddings | Ollama |

---

## Project Structure

```
.
├── main.go                  # Server entry point
├── cli/
│   ├── root.go              # Root Cobra command
│   ├── event.go             # Avro subcommand (event write / event read)
│   ├── serve.go             # Serve subcommand (starts the HTTP server)
│   ├── index.go             # Index subcommand (populates Weaviate for search)
│   ├── chat.go              # Chat subcommand (interactive FODMAP/allergen agent)
│   ├── chat-instruction.txt # Embedded instruction template for the interactive chat agent
│   └── fodmap_data.go       # Static FODMAP ingredient database + lookup
│
├── server/
│   ├── server.go            # HTTP server setup and routes
│   ├── handlers.go          # HTTP request handlers
│   ├── auth_handler.go      # Auth endpoints (register, login, refresh, delete)
│   ├── middleware.go         # JWT auth, rate limiting, CORS middleware
│
├── data/
│   ├── data.go              # Archive reading, Parquet write/read
│   │
│   ├── io/
│   │   └── event.go         # Avro OCF read/write helpers
│   │
│   └── schemas/
│       └── schemas.go       # Review + Business structs + Avro EventSchema
│
├── search/
│   ├── weaviate.go          # Weaviate client: schema, batch upsert, nearText/hybrid search
│   ├── pinecone.go          # Pinecone client: REST-based query, upsert, BM25 re-ranking
│   ├── bm25.go              # BM25 keyword scoring and score blending for hybrid search
│   └── embedder_ollama.go   # Go client for Ollama embeddings API
│
├── auth/
│   ├── store.go             # Unified Store interface
│   ├── sqlite_store.go      # SQLite implementation
│   ├── postgres_store.go    # PostgreSQL implementation
│   ├── jwt.go               # Token generation/validation
│   └── user.go              # User model
│
├── docs/
│   ├── search.md                    # Search service design decisions and API reference
│   ├── chat.md                      # Chat agent design decisions, tradeoffs, and future work
│   └── indexing-improvements.md     # Indexing performance tuning plan
│
└── docker-compose.yaml      # Vector database configuration (Weaviate)
```

---

## Core Data Model

Reviews reference businesses by ID only. The business name and location metadata live in a separate dataset file.

```go
// Review holds a single review record. BusinessID is a foreign key into the business dataset —
// the business name is NOT present here.
type Review struct {
    ReviewID   string  // Yelp review ID
    UserID     string  // Reviewer user ID
    BusinessID string  // Foreign key — look up name/location in Business
    Stars      float32 // Rating (1-5)
    Useful     int32   // Usefulness votes
    Funny      int32   // Funny votes
    Cool       int32   // Cool votes
    Text       string  // Full review text
}

// Business holds metadata from yelp_academic_dataset_business.json.
// Required to resolve a BusinessID to a human-readable name.
type Business struct {
    BusinessID string // Primary key, matches Review.BusinessID
    Name       string // Human-readable restaurant/business name
    City       string
    State      string
    Categories string // Comma-separated, e.g. "Italian, Pizza, Restaurants"
}
```

The Avro streaming schema (`EventSchema`) mirrors the `Review` struct and carries `business_id` but not the business name. During indexing, the name is joined from the business dataset and stored in Weaviate so search results include it directly.

---

## Data Pipeline

```
data/archive.tar.gz  (Yelp JSON lines, gzip-compressed)
        |
        v
   GetArchive(path, "review")  ->  *bufio.Scanner
        |
   |
Avro path (event cmd)
   |
EventWriter.Write()
   |
*.avro
```

---

## Quick Start

The fastest way to get everything running:

```sh
make setup                        # installs Go deps, Ollama, model, Weaviate
export GOOGLE_API_KEY=your_key    # required for chat
make run                          # starts the server on :8081
```

`make setup` works on both **Linux** and **macOS**. It will:
1. Download Go module dependencies
2. Install [golangci-lint](https://golangci-lint.run/) (via Homebrew on macOS, install script on Linux)
3. Install [Ollama](https://ollama.com/) if not present and pull the `nomic-embed-text` embedding model
4. Start Weaviate via Docker Compose

---

## Running

### Prerequisites

- **Docker Engine** with the Compose plugin. On Ubuntu, if `docker compose` is not found:
  ```sh
  sudo apt-get install docker-compose-v2
  ```
- **`GOOGLE_API_KEY`** — required to start the interactive chat agent. Get one from [Google AI Studio](https://aistudio.google.com/app/apikey):
  ```sh
  export GOOGLE_API_KEY=your_key_here
  ```

### Configuration

The project uses [Viper](https://github.com/spf13/viper) for configuration management. You can manage default settings via a `service.yaml` file in the root directory.

#### `service.yaml`

A `service.yaml` file is automatically detected on startup. Command-line flags always take precedence over values defined in the configuration file.

Example `service.yaml`:
```yaml
port: 8081
weaviate: "localhost:8090"
cors-origins:
  - "http://localhost:5173"
chat-model: "gemini-3-flash-preview"
filter-model: "gemini-3.1-flash-lite-preview"
batch-size: 512
workers: 4
```

#### Validation

The CLI performs validation on startup. For example:
- `port` must be between 1 and 65535.
- `batch-size` and `workers` must be greater than 0.
- `chat-model` is required if `chat-api-key` is set.

### 1. Start Vector Search Infrastructure

You must run Weaviate and Ollama to enable semantic search.

*(Note: The chat app will gracefully fall back to BM25 keyword search if the vectorizer isn't running, meaning you only strictly need Ollama for indexing or specialized semantic queries).*

#### Option A: Local Weaviate + Ollama (Recommended)

1. **Start Ollama and Pull the Embedding Model:**
   ```sh
   ollama serve
   ollama pull nomic-embed-text
   ```

2. **Start Weaviate in Docker:**
   ```sh
   docker compose up -d
   ```

3. **Or use `start.sh`** to launch Weaviate and the Go server in one command (Ensure Ollama is running first):
   ```sh
   ./start.sh
   ```

#### Option B: Pinecone (Cloud) + Ollama

Pinecone is a managed vector database. Use this if you want to offload storage to the cloud while keeping embeddings local via Ollama.

1. **Start Ollama:**
   Follow the steps in Option A to start Ollama and pull the model.

2. **Run with Pinecone Flags:**
   ```sh
   go run . serve \
     --pinecone-api-key YOUR_KEY \
     --pinecone-index-host https://YOUR_INDEX.svc.pinecone.io \
     --ollama-url http://localhost:11434 \
     --ollama-model nomic-embed-text
   ```

### 2. Index reviews into Weaviate

```sh
go run . index --weaviate localhost:8090
```

This reads the full Yelp archive, joins reviews with business metadata (city, state, categories),
and upserts them to Weaviate in batches using 4 concurrent upload workers. The command is
idempotent — safe to re-run. A checkpoint file (`index.checkpoint`) is written after each batch so
an interrupted run resumes from where it left off rather than starting over.

```sh
# Custom tuning
go run . index --weaviate localhost:8090 --batch-size 1000 --workers 8

# Resume from a specific checkpoint file
go run . index --checkpoint /tmp/my.checkpoint

# Point to a different archive
go run . index --archive /data/yelp_dataset.tar

# Disable checkpointing
go run . index --checkpoint ""

# Start from a known offset (e.g. after processing 2,155,100 reviews)
go run . index --start-offset 2155100
```

#### GPU-accelerated indexing with Ollama

By default, Weaviate handles vectorization internally using its own transformer. However, passing the Ollama flags bypasses this and pre-vectorizes each batch directly from Go using Ollama:

```sh
go run . index --weaviate localhost:8090 --ollama-url http://localhost:11434 --ollama-model nomic-embed-text
```

**How it works:**

For each batch, all review texts are sent to the embedder concurrently. The resulting vectors are attached to the objects before they are sent to Weaviate. Weaviate detects that each object already has a vector and skips vectorization entirely — it never calls the transformer sidecar during import.

**Tuning GPU utilization:**

The indexing pipeline has three concurrent stages: JSON parsing → Ollama embedding (GPU) → Weaviate upload. The GPU embedding stage is the bottleneck. Each `--batch-size` worth of texts is sent as a single `/api/embed` call to Ollama, so **larger batches = better GPU utilization**.

| Parameter | Default | Effect |
|-----------|---------|--------|
| `--batch-size` | `512` | Texts per GPU embedding call. **Primary tuning lever.** Increase to `1024` or `2048` if VRAM allows. |
| `--workers` | `4` | Concurrent goroutines per pipeline stage. For embedding, this controls parallel requests to Ollama. |
| `OLLAMA_NUM_PARALLEL` | `4` | Ollama's internal request concurrency (set in `start.sh`). Caps the effective `--workers`. |

```sh
# Recommended for GPUs with 8GB+ VRAM
go run . index --weaviate localhost:8090 --batch-size 1024 --workers 4

# Aggressive (large VRAM, e.g. 16GB+)
go run . index --weaviate localhost:8090 --batch-size 2048 --workers 8
```

> **Note:** Increasing `--workers` beyond `OLLAMA_NUM_PARALLEL` just queues requests without improving throughput. If you raise `--workers`, also raise `OLLAMA_NUM_PARALLEL` in `start.sh`. If Ollama returns out-of-memory errors, halve `--batch-size`.

#### Troubleshooting: "no vectorizer found" or vector dimension mismatch

If you see errors like:
```
no vectorizer found for class "YelpReview"
vector lengths don't match: 384 vs 768
```

This means the Weaviate schema was created by a previous run with a different configuration. The fix is to delete the stale classes and re-index:

```sh
# 1. Delete the stale Weaviate classes
curl -X DELETE http://localhost:8090/v1/schema/YelpReview
curl -X DELETE http://localhost:8090/v1/schema/FodmapIngredient

# 2. Re-index (Ollama flags default automatically)
go run . index --weaviate localhost:8090
```

The `index` command defaults to `--ollama-url http://localhost:11434 --ollama-model nomic-embed-text`, so it will pre-vectorize via Ollama as long as Ollama is running.

### 3. Start the HTTP server

```sh
# With search enabled (Weaviate local)
go run . serve --weaviate localhost:8090

# With search enabled (Pinecone Cloud)
go run . serve --pinecone-api-key KEY --pinecone-index-host HOST --ollama-url http://localhost:11434 --ollama-model nomic-embed-text

# Without search (search endpoint returns 503)
go run . serve
```

Default port is `8081`.

#### Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/reviews` | — | List reviews for a business |
| `GET` | `/api/v1/search/businesses/{query...}` | — | Semantic business search (requires Weaviate) |
| `GET` | `/api/v1/search/reviews/{query...}` | — | Semantic review search (requires Weaviate) |
| `POST` | `/api/v1/auth/register` | — | Register a new user account |
| `POST` | `/api/v1/auth/login` | — | Log in and receive access/refresh tokens |
| `POST` | `/api/v1/auth/refresh` | — | Exchange a refresh token for new tokens |
| `POST` | `/api/v1/auth/logout` | JWT | Log out (client-side token discard) |
| `DELETE` | `/api/v1/auth/user` | JWT | Delete the authenticated user's account |
| `GET` | `/api/v1/conversations` | JWT | List conversations |
| `POST` | `/api/v1/conversations` | JWT | Create a new conversation |
| `GET` | `/api/v1/conversations/{id}` | JWT | Get a conversation |
| `DELETE` | `/api/v1/conversations/{id}` | JWT | Delete a conversation |
| `POST` | `/api/v1/conversations/{id}/messages` | JWT | Send a chat message (streaming) |
| `GET` | `/api/v1/profile` | JWT | Get dietary profile |
| `POST` | `/api/v1/profile` | JWT | Update dietary profile |

**Common query parameters** for search endpoints:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `limit` | `10` | Max results to return |
| `category` | — | Filter by cuisine/category substring |
| `city` | — | Filter by city (exact match) |
| `state` | — | Filter by state (exact match) |
| `alpha` | `0` | Hybrid search weight: `0`=pure vector, `0.75`=balanced, `1`=pure vector |

#### Authentication

The server uses JWT-based authentication. Access tokens expire after **2 hours**; refresh tokens last **7 days**.

```sh
# Register
curl -X POST localhost:8081/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email": "user@example.com", "password": "mypassword"}'

# Login
curl -X POST localhost:8081/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email": "user@example.com", "password": "mypassword"}'
# → {"access_token": "...", "refresh_token": "...", "user": {...}}

# Use the access token for protected endpoints
curl -H 'Authorization: Bearer <access_token>' localhost:8081/api/v1/conversations

# Refresh tokens
curl -X POST localhost:8081/api/v1/auth/refresh \
  -H 'Content-Type: application/json' \
  -d '{"refresh_token": "..."}'

# Delete account (soft delete — the user is marked as deleted and cannot log in again)
curl -X DELETE -H 'Authorization: Bearer <access_token>' localhost:8081/api/v1/auth/user
# → {"message": "account deleted"}
```

> **Note:** Account deletion is a soft delete — the user's status is set to `"deleted"` and they are
> blocked from logging in or refreshing tokens. Existing access tokens remain valid until they expire
> (up to 2 hours). User data (conversations, messages) is retained for potential recovery.

#### Search endpoints

Find restaurants or review texts matching a natural-language description:

```sh
# Business search — returns top 10 businesses ranked by review relevance
curl "localhost:8081/api/v1/search/businesses/cozy%20Italian%20with%20great%20pasta"

# Filter by category, city, state
curl "localhost:8081/api/v1/search/businesses/best%20tacos?category=Mexican&city=Las%20Vegas&state=NV&limit=5"
```

##### Hybrid search (`?alpha=`)

All search endpoints support an optional `alpha` parameter that controls the balance between semantic vector search and BM25 keyword search:

| `alpha` value | Behaviour |
|--------------|-----------|
| Omitted / `0` | Pure semantic vector search (default, backward-compatible) |
| `0.0`–`1.0` | Hybrid: blend of BM25 and vector (higher = more vector weight) |
| `1.0` | Pure semantic vector search |

```sh
# Hybrid search: 75% vector + 25% BM25 keyword
curl "localhost:8081/api/v1/search/businesses/gluten%20free%20pizza?alpha=0.75"

# Heavily keyword-weighted (good for exact dish/ingredient names)
curl "localhost:8081/api/v1/search/reviews/pad%20thai?alpha=0.2"
```

On Weaviate, hybrid search uses the native `hybrid` operator with `relativeScoreFusion`. On Pinecone, BM25 re-ranking is applied in-process against the review `text` metadata field and blended with the dense vector score.

See [docs/search.md](docs/search.md) for full API reference and design decisions.

### CLI

Run the CLI with:

```sh
go run .
```

#### Commands

##### Index (Weaviate)

```sh
go run . index --weaviate localhost:8090
```

| Flag | Default | Description |
|------|---------|-------------|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--batch-size` | `512` | Reviews per batch |
| `--workers` | `4` | Concurrent batch upload goroutines |
| `--archive` | `../data/yelp_dataset.tar` | Path to the Yelp dataset TAR archive |
| `--ollama-url` | `""` | Ollama server URL (e.g. `http://localhost:11434`) |
| `--ollama-model` | `""` | Ollama embedding model (e.g. `nomic-embed-text`) |

##### Chat (interactive FODMAP/allergen agent)

```sh
# Find the top Thai restaurant in Las Vegas and start a chat about its dishes
GOOGLE_API_KEY=${GEMINI_KEY} go run . chat "pad thai" --city "Las Vegas" --state NV
```

See [docs/chat.md](docs/chat.md) for design decisions and tradeoffs.

---

## Testing

The project maintains a high test coverage standard (minimum **70%** for non-CLI packages) to ensure reliability.

### Running Tests

```bash
# Run all tests
go test ./...

# Run tests and generate coverage report
go test -coverprofile=coverage.out ./...

# View coverage summary by function
go tool cover -func=coverage.out
```

### Coverage Threshold (CI)

The GitHub Actions pipeline is configured to enforce a 70% coverage threshold. If total coverage (excluding the `cli/` package) drops below this level, the build will fail. 

To run the same check locally:
```bash
go test ./... -coverprofile=coverage.out
grep -v "fodmap/cli" coverage.out > coverage_filtered.out
go tool cover -func=coverage_filtered.out | grep total:
```

### Mocking

Many tests (especially in `chat/` and `search/`) use `httptest.Server` to mock external APIs like Gemini and Weaviate. This allows for fast, deterministic unit testing without requiring real API keys or running infrastructure.

---

## Input Data

Place the Yelp dataset archive at:

```
./data/archive.tar.gz
```

The archive must contain files whose names include `"review"` and `"business"`:
- `yelp_academic_dataset_review.json` — review text and ratings (required for all features)
- `yelp_academic_dataset_business.json` — business name, city, state, categories (required for search filters)

Both files must be formatted as newline-delimited JSON (JSONL).
