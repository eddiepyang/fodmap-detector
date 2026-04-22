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
| Embeddings | In-process native Go vectorizer via llama.cpp (llama-go) |

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
│   ├── embedder_llama.go    # Native Go in-process embedding generation via llama.cpp
│   └── vectorizer.go        # HTTP client for external vectorizer APIs (fallback)
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

You must run Weaviate to enable semantic search. The native Go vectorizer uses `llama-go` in-process, meaning embeddings are generated efficiently using hardware acceleration (Metal/MPS on macOS, CUDA on Linux) without needing an external Python service.

*(Note: The chat app will gracefully fall back to BM25 keyword search if vectorization is not configured).*

**Important Build Requirement:** Because `llama-go` relies on a C++ submodule (`llama.cpp`), you must build the C/C++ bindings locally before running the server for the first time. You will need `cmake` installed (e.g. `brew install cmake`).

Run the setup command once:
```sh
make setup-llama
```

#### Option A: Local Weaviate + Native Vectorizer

Weaviate runs in Docker, but all embeddings are generated natively in Go using `llama.cpp`.

1. **Start Weaviate in Docker:**
   ```sh
   docker compose up -d
   ```
   This starts Weaviate on port `8090`.

2. **Or use `start.sh`** to launch everything (Weaviate + Go server) in one command:
   ```sh
   ./start.sh
   ```

#### Option B: Pinecone (Cloud)

Pinecone is a managed vector database. Use this if you want to offload storage to the cloud while keeping embeddings local.

1. **Run with Pinecone Flags:**
   ```sh
   go run -tags llamago . serve \
     --pinecone-api-key YOUR_KEY \
     --pinecone-index-host https://YOUR_INDEX.svc.pinecone.io \
     --model-path models/nomic-embed-text-v1.5.Q5_K_M.gguf
   ```

*(On macOS, CGO flags for Metal acceleration are automatically applied via directives in the source code when building with the `-tags llamago` flag).*

### 2. Index reviews into Weaviate

```sh
go run -tags llamago . index --weaviate localhost:8090 --model-path models/nomic-embed-text-v1.5.Q5_K_M.gguf
```

This reads the full Yelp archive, joins reviews with business metadata (city, state, categories),
and upserts them to Weaviate in batches using 4 concurrent upload workers. The command is
idempotent — safe to re-run. A checkpoint file (`index.checkpoint`) is written after each batch so
an interrupted run resumes from where it left off rather than starting over.

```sh
# Custom tuning
go run -tags llamago . index --weaviate localhost:8090 --model-path models/nomic-embed-text-v1.5.Q5_K_M.gguf --batch-size 1000 --workers 8

# Resume from a specific checkpoint file
go run -tags llamago . index --checkpoint /tmp/my.checkpoint --model-path models/nomic-embed-text-v1.5.Q5_K_M.gguf

# Point to a different archive
go run -tags llamago . index --archive /data/yelp_dataset.tar --model-path models/nomic-embed-text-v1.5.Q5_K_M.gguf

# Disable checkpointing
go run -tags llamago . index --checkpoint "" --model-path models/nomic-embed-text-v1.5.Q5_K_M.gguf

# Start from a known offset (e.g. after processing 2,155,100 reviews)
go run -tags llamago . index --start-offset 2155100 --model-path models/nomic-embed-text-v1.5.Q5_K_M.gguf
```

#### GPU-accelerated indexing

Weaviate does not vectorize objects itself. Vectorization is performed in-process via the `llama-go` embedder by providing `--model-path`. For each batch, all review texts are generated concurrently using the local GGUF model, utilizing hardware acceleration (Metal/CUDA) directly.

### 3. Start the HTTP server

```sh
# With search enabled (Weaviate local)
go run -tags llamago . serve --weaviate localhost:8090 --model-path models/nomic-embed-text-v1.5.Q5_K_M.gguf

# With search enabled (Pinecone Cloud)
go run -tags llamago . serve --pinecone-api-key KEY --pinecone-index-host HOST --model-path models/nomic-embed-text-v1.5.Q5_K_M.gguf

# Without search (search endpoint returns 503)
go run . serve
```

Default port is `8081`.

#### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/reviews` | List reviews for a business |
| `GET` | `/api/v1/search/businesses/{query...}` | Semantic business search — returns ranked restaurants (requires Weaviate) |
| `GET` | `/api/v1/search/reviews/{query...}` | Semantic review search — returns top matching review texts (requires Weaviate) |

**Common query parameters** for search endpoints:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `limit` | `10` | Max results to return |
| `category` | — | Filter by cuisine/category substring |
| `city` | — | Filter by city (exact match) |
| `state` | — | Filter by state (exact match) |
| `alpha` | `0` | Hybrid search weight: `0`=pure vector, `0.75`=balanced, `1`=pure vector |

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
go run -tags llamago . index --weaviate localhost:8090 --model-path models/nomic-embed-text-v1.5.Q5_K_M.gguf
```

| Flag | Default | Description |
|------|---------|-------------|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--batch-size` | `512` | Reviews per batch |
| `--workers` | `4` | Concurrent batch upload goroutines |
| `--archive` | `../data/yelp_dataset.tar` | Path to the Yelp dataset TAR archive |
| `--model-path` | `""` | Path to GGUF embedding model for in-process vectorization |

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
