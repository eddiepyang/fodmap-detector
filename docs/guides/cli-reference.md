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
  --llm-url http://localhost:11434/v1 \
  --llm-model qwen3.6:35b-mlx \
  --enable-vision

# Route PDF/OCR extraction to the Python scraper service (handles vector-outline
# and scanned PDFs that pure-Go vision can't). The service must be running
# (e.g. cd ../scraper && uv run uvicorn scraper.app:app --port 8765).
go run . scrape "https://example-restaurant.com/menu.pdf" \
  --weaviate localhost:8090 \
  --llm-url http://localhost:11434/v1 \
  --extractor-url http://localhost:8765

# Route JS-rendered pages to the service's webagent (Phase B). Requires a
# pre-compiled adapter (see ../scraper/src/scraper/webagent/discovery/cli.py).
go run . scrape "https://js-heavy-site.com/menu" \
  --weaviate localhost:8090 \
  --llm-url http://localhost:11434/v1 \
  --extractor-url http://localhost:8765 \
  --enable-js-render --webagent-adapter site/target
```

| Flag | Default | Description |
|------|---------|-------------|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--llm-url` | `http://localhost:11434/v1` | Base URL for OpenAI-compatible LLM endpoint |
| `--llm-model` | `qwen3.6:35b-mlx` | LLM model to use |
| `--enable-vision` | `false` | Send PDFs/images to the local vision LLM (pure-Go fallback; alternative to `--extractor-url`) |
| `--pdftotext` | `false` | Fall back to system `pdftotext` (poppler) for PDF text extraction |
| `--extractor-url` | `""` | Base URL of the Python scraper service for PDF/OCR (e.g. `http://localhost:8765`); empty = pure-Go default |
| `--extractor-page-timeout` | `2m` | Per-page request timeout when calling the scraper service |
| `--extractor-pdf-timeout` | `10m` | Overall PDF deadline when calling the scraper service |
| `--enable-js-render` | `false` | Route noisy/JS-only HTML pages to the webagent (requires `--extractor-url` + `--webagent-adapter`) |
| `--webagent-adapter` | `""` | webagent adapter ID (`site/target`) for JS-rendered pages |

**Config ownership split:** when `--extractor-url` is set, PDF structuring is
owned by the service's `SCRAPER_LLM_*` / OCR backend config — the detector's
`--llm-model`/`--llm-url` only drive the HTML/text path (embeddings remain on
`--ollama-*`). See `docs/plans/scraper-service-integration-plan.md` for details.

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

