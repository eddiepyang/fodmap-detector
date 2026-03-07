# FODMAP Detector

A Go CLI tool that processes Yelp dataset reviews to identify FODMAP (Fermentable Oligosaccharides, Disaccharides, Monosaccharides, and Polyols) content in food items. Designed to help people with digestive sensitivities by analyzing restaurant reviews for dish ingredients and flagging FODMAP groups.

---

## Purpose

1. Read Yelp review data from a compressed archive (`.tar.gz` of JSON lines)
2. Serialize reviews to Apache Avro (streaming) or Apache Parquet (columnar batch) formats
3. (In progress) Run LLM-based FODMAP analysis on review text using a structured prompt

---

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.23+ |
| CLI | [Cobra](https://github.com/spf13/cobra) |
| Streaming format | Apache Avro (OCF) via [hamba/avro](https://github.com/hamba/avro) |
| Batch format | Apache Parquet via [xitongsys/parquet-go](https://github.com/xitongsys/parquet-go) |
| Input | TAR.GZ compressed JSON lines (Yelp dataset) |
| Concurrency | Go channels + goroutines |

---

## Project Structure

```
.
├── main.go                  # Server entry point
├── cmd/
│   └── cli/
│       └── main.go          # CLI entry point
├── flags.go                 # Global CLI flags (--model)
├── prompt.txt               # LLM prompt for FODMAP extraction (WIP)
│
├── cli/
│   ├── root.go              # Root Cobra command
│   ├── event.go             # Avro subcommand (event write / event read)
│   ├── batch.go             # Parquet subcommand (batch)
│   └── serve.go             # Serve subcommand (starts the HTTP server)
│
├── server/
│   ├── server.go            # HTTP server setup and routes
│   ├── handlers.go          # HTTP request handlers
│   ├── jobs.go              # Background job store
│   └── llm.go               # Gemini LLM client
│
├── data/
│   ├── data.go              # Core pipeline: archive reading, Parquet write/read
│   ├── constants.go         # Schema constants and file paths
│   │
│   ├── io/
│   │   ├── batch.go         # Channel-based JSON reader (ReadToChan)
│   │   └── event.go         # Avro OCF read/write helpers
│   │
│   └── schemas/
│       └── schemas.go       # ReviewSchemaS struct + Avro EventSchema
│
└── vendor/                  # Vendored dependencies
```

---

## Core Data Model

```go
type ReviewSchemaS struct {
    ReviewId   string  // Yelp review ID
    UserId     string  // Reviewer user ID
    BusinessId string  // Restaurant/business ID
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
WriteEventFile()        WriteBatchParquet()
   |                         |
*.avro                  *.parquet
                             |
                        ReadParquet()
                             |
                      []ReviewSchemaS
```

---

## Running

### Server

Start the HTTP analysis server:

```sh
go run .
```

Default port is `8080`. Default prompt path is `./prompt.txt`.

#### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/analyze` | Submit reviews for FODMAP analysis (returns job ID) |
| `GET` | `/results/{job_id}` | Poll analysis results |
| `GET` | `/reviews` | List available reviews |

### CLI

Run the CLI with:

```sh
go run ./cmd/cli
```

#### Commands

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

The archive must contain a file whose name includes `"review"` (e.g. `yelp_academic_dataset_review.json`), formatted as newline-delimited JSON (JSONL).
