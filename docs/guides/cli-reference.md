# CLI Reference

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

##### Scrape (Menu Extraction)

Scrape a restaurant menu page (HTML or PDF), extract the dishes and ingredients using an LLM, and index them into Weaviate for the chat agent to use.

```sh
# Basic scrape (HTML to Markdown via LLM)
go run . scrape "https://example-restaurant.com/menu" --weaviate localhost:8090

# Scrape an image-based menu or PDF using a local Vision LLM (e.g. Qwen3.6 via Ollama)
go run . scrape "https://example-restaurant.com/menu.pdf" \
  --weaviate localhost:8090 \
  --llm-backend openai-compat \
  --llm-url http://localhost:11434 \
  --llm-model qwen3.6:35b-mlx \
  --enable-vision

# Route scanned PDFs to the Python vision service (lossless structured extraction)
go run . scrape "https://example-restaurant.com/scanned-menu.pdf" \
  --store postgres \
  --postgres-dsn "postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable" \
  --extractor-url http://localhost:8765
```

| Flag | Default | Description |
|------|---------|-------------|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--llm-backend` | `openai-compat` | `openai-compat` or `gemini` |
| `--llm-url` | `http://localhost:11434` | Base URL for openai-compat backend |
| `--llm-model` | `qwen3.6:35b-mlx` | LLM model to use |
| `--enable-vision` | `false` | Send PDFs/images to the vision LLM instead of text extraction |
| `--pdftotext` | `false` | Fall back to system `pdftotext` (poppler) for PDF text extraction |
| `--extractor-url` | `""` | Python vision service base URL; when set, scanned PDFs/images route to the service instead of the Go LLM path |
| `--store` | `weaviate` | Storage backend: `weaviate` \| `postgres` |
| `--postgres-dsn` | `""` | PostgreSQL DSN (required when `--store=postgres`) |

##### Chat (interactive FODMAP/allergen agent)

```sh
# Find the top Thai restaurant in Las Vegas and start a chat about its dishes
GOOGLE_API_KEY=${GEMINI_KEY} go run . chat "pad thai" --city "Las Vegas" --state NV
```

See [chat.md](chat.md) for design decisions and tradeoffs.

##### Database Migrations

```sh
# Run all pending domain and river migrations
go run . db migrate-up

# Roll back one migration step
go run . db migrate-down

# Force-set migration version (for existing databases pre-golang-migrate)
go run . db migrate-force 1

# Print current migration version
go run . db migrate-version
```

| Flag | Default | Description |
|------|---------|-------------|
| `--postgres-dsn` | `POSTGRES_DSN` env | PostgreSQL connection string (required) |

---

