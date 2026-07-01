# Quick Start & Setup

## Quick Start

The fastest way to get everything running:

```sh
make setup                        # installs Go deps, Ollama, model, Weaviate
export GEMINI_API_KEY=your_key    # required for chat
make run                          # starts the server on :8081
```

`make setup` works on both **Linux** and **macOS**. It will:
1. Download Go module dependencies
2. Install [golangci-lint](https://golangci-lint.run/) (via Homebrew on macOS, install script on Linux)
3. Install [Ollama](https://ollama.com/) if not present and pull the `nomic-embed-text` embedding model
4. Start Weaviate and PostgreSQL via Docker Compose

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
# postgres-search: true          # Enable PostgreSQL/pgvector search backend
# postgres-dsn: "postgres://user:pass@localhost:5432/fodmap?sslmode=disable"
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

3. **Run database migrations** (creates all domain tables):
   ```sh
   export POSTGRES_DSN="postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable"
   go run . db migrate-up
   ```

4. **Or use `start.sh`** to launch Weaviate, run migrations, and start the Go server in one command (Ensure Ollama is running first):
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
     --postgres-dsn "postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable" \
     --pinecone-api-key YOUR_KEY \
     --pinecone-index-host https://YOUR_INDEX.svc.pinecone.io \
     --ollama-url http://localhost:11434 \
     --ollama-model nomic-embed-text
   ```

#### Option C: PostgreSQL (pgvector) + Ollama

Use PostgreSQL with the [pgvector](https://github.com/pgvector/pgvector) extension for vector search. This is a good option if you already have PostgreSQL running and want to keep everything in one database.

1. **Start Ollama:**
   Follow the steps in Option A to start Ollama and pull the model.

2. **Set up PostgreSQL with pgvector:**
   ```sql
   CREATE EXTENSION IF NOT EXISTS vector;
   ```

3. **Run with PostgreSQL Flags:**
   ```sh
   go run . serve \
     --postgres-search \
     --postgres-dsn "postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable" \
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


