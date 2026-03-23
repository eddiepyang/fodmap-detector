# FODMAP Detector

A Go CLI tool that processes Yelp dataset reviews to identify FODMAP (Fermentable Oligosaccharides, Disaccharides, Monosaccharides, and Polyols) content in food items. Designed to help people with digestive sensitivities by analyzing restaurant reviews for dish ingredients and flagging FODMAP groups.

---

## Purpose

1. Read Yelp review data from a compressed archive (`.tar.gz` of JSON lines)
2. Serialize reviews to Apache Avro (streaming) or Apache Parquet (columnar batch) formats
3. Run LLM-based FODMAP analysis on review text using Google Gemini

---

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.26+ |
| CLI | [Cobra](https://github.com/spf13/cobra) |
| Streaming format | Apache Avro (OCF) via [hamba/avro](https://github.com/hamba/avro) |
| Batch format | Apache Parquet via [xitongsys/parquet-go](https://github.com/xitongsys/parquet-go) |
| Input | TAR.GZ compressed JSON lines (Yelp dataset) |
| Concurrency | Go channels + goroutines |
| Vector search | [Weaviate](https://weaviate.io) with `text2vec-transformers` (local embeddings) |

---

## Project Structure

```
.
├── main.go                  # Server entry point
├── cmd/
│   └── cli/
│       └── main.go          # CLI entry point
├── prompt.txt               # LLM prompt for FODMAP extraction
│
├── cli/
│   ├── root.go              # Root Cobra command
│   ├── event.go             # Avro subcommand (event write / event read)
│   ├── batch.go             # Parquet subcommand (batch)
│   ├── serve.go             # Serve subcommand (starts the HTTP server)
│   └── index.go             # Index subcommand (populates Weaviate for search)
│
├── server/
│   ├── server.go            # HTTP server setup and routes
│   ├── handlers.go          # HTTP request handlers
│   ├── jobs.go              # Background job store
│   └── llm.go               # Gemini LLM client
│
├── data/
│   ├── data.go              # Archive reading, Parquet write/read
│   │
│   ├── io/
│   │   ├── batch.go         # Channel-based JSON reader (ReadToChan)
│   │   └── event.go         # Avro OCF read/write helpers
│   │
│   └── schemas/
│       └── schemas.go       # Review + Business structs + Avro EventSchema
│
├── search/
│   └── weaviate.go          # Weaviate client: schema, batch upsert, nearText search
│
├── docs/
│   ├── search.md                    # Search service design decisions and API reference
│   └── indexing-improvements.md     # Indexing performance tuning plan
│
└── docker-compose.yaml      # Weaviate + t2v-transformers services
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
   +----+--------------------+
   |                         |
Avro path               Parquet path
(event cmd)             (batch cmd)
   |                         |
EventWriter.Write()     WriteBatchParquet()
   |                         |
*.avro                  *.parquet
                             |
                        ReadParquet()
                             |
                        []Review
```

---

## Running

### Prerequisites

- **Docker Engine** with the Compose plugin. On Ubuntu, if `docker compose` is not found:
  ```sh
  sudo apt-get install docker-compose-v2
  ```
- **`GEMINI_API_KEY`** — required to start the HTTP server (used by the `/analyze` endpoint). Get one from [Google AI Studio](https://aistudio.google.com/app/apikey):
  ```sh
  export GEMINI_API_KEY=your_key_here
  ```

### 1. Start Weaviate (required for search)

```sh
docker compose up -d
```

This starts:
- **Weaviate** on port `8090` — the vector database
- **t2v-transformers** on port `8091` — a local `sentence-transformers/multi-qa-MiniLM-L6-cos-v1` inference server used to embed review text into 384-dimensional vectors

On first run, the transformer model (~90 MB) is downloaded automatically. Wait for:

```
t2v-transformers  | Application startup complete.
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

`GEMINI_API_KEY` must be set in the environment before starting.

```sh
# With search enabled
GEMINI_API_KEY=your_key go run . serve --weaviate localhost:8090

# Without search (search endpoint returns 503)
GEMINI_API_KEY=your_key go run . serve
```

Default port is `8080`. Default prompt path is `./prompt.txt`.

#### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/analyze` | Submit reviews for FODMAP analysis (returns job ID) |
| `GET` | `/results/{job_id}` | Poll analysis results |
| `GET` | `/reviews` | List reviews for a business |
| `GET` | `/search/{query...}` | Semantic restaurant search (requires Weaviate) |

#### Search endpoint

Find restaurants matching a natural-language description:

```sh
# Basic search — returns top 10 businesses by relevance
curl "localhost:8080/search/cozy%20Italian%20with%20great%20pasta"

# Filter by category
curl "localhost:8080/search/best%20tacos?category=Mexican"

# Filter by city and state
curl "localhost:8080/search/romantic%20dinner?city=Las%20Vegas&state=NV"

# Combine all filters with a custom limit
curl "localhost:8080/search/outdoor%20patio%20brunch?category=Breakfast&city=Phoenix&state=AZ&limit=5"
```

**Response:**
```json
{
  "businesses": [
    {"id": "abc123", "name": "Joe's Diner"},
    {"id": "def456", "name": "Pasta Palace"},
    {"id": "ghi789", "name": "Taco Town"}
  ]
}
```

Business IDs are ranked by **Top-K average similarity** — the average of the top 5 most relevant
reviews per restaurant. This avoids both volume bias (popular chains don't dominate) and outlier
noise (one lucky review can't carry a poor fit).

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
| `--checkpoint` | `index.checkpoint` | Checkpoint file path (empty string disables) |
| `--start-offset` | `0` | Skip this many reviews before indexing (overrides checkpoint) |
| `--vectorizer` | `""` | t2v-transformers host:port for direct pre-vectorization (e.g. `localhost:8091`); omit to let Weaviate vectorize |

##### Parquet (batch)

```sh
# Write reviews from archive to Parquet
go run . batch -o output.parquet

# Limit to 500 records
go run . batch -o output.parquet -n 500
```

| Flag | Default | Description |
|------|---------|-------------|
| `-o, --output` | `test.parquet` | Output file path |
| `-n, --limit` | `0` | Max records to write (0 = no limit) |

##### Avro (event)

```sh
# Write reviews from archive to Avro OCF
go run . event write -o output.avro

# Limit to 500 records
go run . event write -o output.avro -n 500

# Read and dump an Avro file
go run . event read -i output.avro
```

| Flag | Default | Description |
|------|---------|-------------|
| `-o, --output` | `test.avro` | Output file path |
| `-n, --limit` | `0` | Max records to write (0 = no limit) |

##### Global flag

```sh
-m, --model <string>   Model name (for future LLM integration)
```

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
