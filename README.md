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
| Embeddings | Locally-run transformers via Python proxy or internal Weaviate |

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
│   ├── weaviate.go          # Weaviate client: schema, batch upsert, nearText search
│   ├── pinecone.go          # Pinecone client: REST-based query and upsert
│   └── vectorizer.go        # Go client for the local embedding server (JSON/Binary)
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
├── docker-compose.yaml      # Base: Weaviate only (vectorizer runs natively)
└── docker-compose.gpu.yaml  # Override: adds CUDA vectorizer in Docker
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
- **`GEMINI_API_KEY`** — required to start the interactive chat agent. Get one from [Google AI Studio](https://aistudio.google.com/app/apikey):
  ```sh
  export GEMINI_API_KEY=your_key_here
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

You must run Weaviate and the embedding vectorizer to enable semantic search. The setup differs depending on whether you want to use Mac Apple Silicon (Metal/MPS) or a Linux machine with an NVIDIA GPU.

*(Note: The chat app will gracefully fall back to BM25 keyword search if the vectorizer isn't running, meaning you only strictly need the python vectorizer for indexing or specialized semantic queries).*

#### Option A: Mac (Apple Silicon / MPS) or CPU

Docker on macOS cannot access Metal/MPS hardware, so the vectorizer must run natively to use GPU acceleration. Weaviate runs in Docker and connects back to the host.

1. **Start Weaviate in Docker:**
   ```sh
   docker compose up -d
   ```
   This starts Weaviate on port `8090`. It reaches the vectorizer via `host.docker.internal:8080` (resolved automatically on macOS Docker Desktop; on Linux, `extra_hosts` in the compose file handles this).

2. **Start the Vectorizer Natively:**
   ```sh
   cd vectorizer-proxy
   conda activate torch-env   # or use a venv
   pip install -r requirements.txt
   uvicorn app:app --host 0.0.0.0 --port 8080
   ```
   The vectorizer auto-detects the best device: MPS on Apple Silicon, CUDA if available, otherwise CPU.

3. **Or use `start.sh`** to launch everything (Weaviate + vectorizer + Go server) in one command:
   ```sh
   ./start.sh
   ```

#### Option B: Linux with NVIDIA GPU (Docker)

With `nvidia-container-toolkit` installed, run everything inside Docker — the vectorizer gets direct GPU passthrough via the override compose file:

```sh
docker compose -f docker-compose.yaml -f docker-compose.gpu.yaml up -d
```

This starts both Weaviate and the CUDA-accelerated vectorizer (with FP16 inference) entirely in Docker. No need to run the Python script natively.

**Vectorizer tuning** (set in `docker-compose.gpu.yaml` or as env vars):

| Variable | Default | Description |
|----------|---------|-------------|
| `BATCH_MAX_SIZE` | `64` | Max requests batched into a single GPU forward pass |
| `BATCH_TIMEOUT` | `0.01` | Seconds to wait for more requests before encoding (10ms) |
| `ENABLE_CUDA` | `1` | Set to `1` to use CUDA; the vectorizer also enables FP16 on CUDA devices |

The vectorizer automatically batches concurrent `/vectors` requests within the timeout window, so the GPU processes multiple texts per kernel launch instead of one at a time.

#### Option C: Pinecone (Cloud + Local Vectorizer)

Pinecone is a managed vector database. Use this if you want to offload storage to the cloud while keeping embeddings local.

1. **Start the Vectorizer (Native or Docker):**
   Follow the steps in Option A or B to start the `vectorizer-proxy`.

2. **Run with Pinecone Flags:**
   ```sh
   go run . serve \
     --pinecone-api-key YOUR_KEY \
     --pinecone-index-host https://YOUR_INDEX.svc.pinecone.io \
     --vectorizer-url http://localhost:8000
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

#### GPU-accelerated indexing with `--vectorizer`

By default, Weaviate handles vectorization internally: it calls the t2v-transformers sidecar once
per object, sequentially. This means the GPU always receives a batch of one, leaving it heavily
underutilized (~35% on typical hardware).

Passing `--vectorizer` bypasses this and pre-vectorizes each batch directly from Go:

```sh
go run . index --weaviate localhost:8090 --vectorizer localhost:8091
```

**How it works:**

For each batch, all review texts are sent to the transformer concurrently (one goroutine per text).
The transformer is configured with `BATCH_WAIT_TIME_SECONDS=0.1` and `MAX_BATCH_SIZE=512`, so it
accumulates the concurrent requests that arrive within 100 ms and runs them as a single GPU forward
pass — giving the GPU a batch of up to 512 instead of 1. The resulting vectors are attached to the
objects before they are sent to Weaviate. Weaviate detects that each object already has a vector
and skips vectorization entirely — it never calls the transformer sidecar during import.

Without `--vectorizer`, indexing still works correctly — Weaviate vectorizes each object itself.
The flag only affects throughput, not correctness, so it can be omitted on CPU-only machines.

### 3. Start the HTTP server

```sh
# With search enabled (Weaviate local)
go run . serve --weaviate localhost:8090

# With search enabled (Pinecone Cloud)
go run . serve --pinecone-api-key KEY --pinecone-index-host HOST --vectorizer-url http://localhost:8000

# Without search (search endpoint returns 503)
go run . serve
```

Default port is `8081`.

#### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/reviews` | List reviews for a business |
| `GET` | `/searchBusiness/{query...}` | Semantic business search — returns ranked restaurants (requires Weaviate) |
| `GET` | `/searchReview/{query...}` | Semantic review search — returns top matching review texts (requires Weaviate) |

#### Search endpoints

Find restaurants or review texts matching a natural-language description:

```sh
# Business search — returns top 10 businesses ranked by review relevance
curl "localhost:8081/searchBusiness/cozy%20Italian%20with%20great%20pasta"

# Filter by category, city, state
curl "localhost:8081/searchBusiness/best%20tacos?category=Mexican&city=Las%20Vegas&state=NV&limit=5"
```

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
| `--vectorizer` | `""` | t2v-transformers host:port for direct pre-vectorization |

##### Chat (interactive FODMAP/allergen agent)

```sh
# Find the top Thai restaurant in Las Vegas and start a chat about its dishes
GEMINI_API_KEY=${GEMINI_KEY} go run . chat "pad thai" --city "Las Vegas" --state NV
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
