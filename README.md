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
│   └── search.md            # Search service design decisions and API reference
│
└── docker-compose.yaml      # Weaviate + t2v-transformers services
```

---

## Core Data Model

```go
type Review struct {
    ReviewID   string  // Yelp review ID
    UserID     string  // Reviewer user ID
    BusinessID string  // Restaurant/business ID
    Stars      float32 // Rating (1-5)
    Useful     int32   // Usefulness votes
    Funny      int32   // Funny votes
    Cool       int32   // Cool votes
    Text       string  // Full review text
}
```

---

## Data Pipeline

```
data/archive.tar.gz  (Yelp JSON lines, gzip-compressed)
        |
        v
   GetArchive("review")  ->  *bufio.Scanner
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

Docker Engine with the Compose plugin is required. On Ubuntu, if `docker compose` is not found:

```sh
sudo apt-get install docker-compose-v2
```

### 1. Start Weaviate (required for search)

```sh
docker compose up -d
```

This starts Weaviate on port `8090` and the `text2vec-transformers` inference sidecar.
On first run, the transformer model (~90 MB) is downloaded automatically. Wait for:

```
t2v-transformers  | Application startup complete.
```

### 2. Index reviews into Weaviate

```sh
go run ./cmd/cli index --weaviate localhost:8090
```

This reads the full Yelp archive, joins reviews with business metadata (city, state, categories),
and upserts them to Weaviate in batches of 100. The command is idempotent — safe to re-run.

```sh
# Custom batch size
go run ./cmd/cli index --weaviate localhost:8090 --batch-size 500
```

### 3. Start the HTTP server

```sh
# With search enabled
go run . serve --weaviate localhost:8090

# Without search (search endpoint returns 503)
go run . serve
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
# Basic search — returns top 10 business IDs by relevance
curl "localhost:8080/search/cozy Italian with great pasta"

# Filter by category
curl "localhost:8080/search/best tacos?category=Mexican"

# Filter by city and state
curl "localhost:8080/search/romantic dinner?city=Las Vegas&state=NV"

# Combine all filters with a custom limit
curl "localhost:8080/search/outdoor patio brunch?category=Breakfast&city=Phoenix&state=AZ&limit=5"
```

**Response:**
```json
{
  "business_ids": ["abc123", "def456", "ghi789"]
}
```

Business IDs are ranked by **Top-K average similarity** — the average of the top 5 most relevant
reviews per restaurant. This avoids both volume bias (popular chains don't dominate) and outlier
noise (one lucky review can't carry a poor fit).

See [docs/search.md](docs/search.md) for full API reference and design decisions.

### CLI

Run the CLI with:

```sh
go run ./cmd/cli
```

#### Commands

##### Index (Weaviate)

```sh
go run ./cmd/cli index --weaviate localhost:8090
```

| Flag | Default | Description |
|------|---------|-------------|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--batch-size` | `100` | Reviews per batch |

##### Parquet (batch)

```sh
# Write reviews from archive to Parquet, then read back 5 rows
fodmap-detector batch -o output.parquet
```

##### Avro (event)

```sh
# Write reviews from archive to Avro OCF
fodmap-detector event write -o output.avro

# Read and dump an Avro file
fodmap-detector event read -i output.avro
```

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
