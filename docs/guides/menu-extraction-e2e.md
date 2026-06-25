# Menu Extraction — End-to-End Testing Guide

This guide walks through manually testing the Phase 2 menu-extraction pipeline:
the Python vision service (`--extractor-url`) path, the Go LLM vision fallback
(`--enable-vision`), and the text-layer PDF path, all writing into Postgres.

---

## Prerequisites

- Docker Engine with Compose plugin (`docker compose version`)
- Go 1.26+ (`go version`)
- Python 3.12+ with `uv` (`uv --version`); only needed to run the service locally
- `psql` for inspecting results (`psql --version`)
- A PDF to test with — ideally one scanned (image-only) menu and one with a text layer

---

## 1. Start Infrastructure

```sh
# Postgres + Weaviate
docker compose up -d

# Run migrations (creates restaurant_menu table among others)
POSTGRES_DSN="postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable" \
  go run . db migrate-up
```

---

## 2. Start the Python Extractor Service

**Option A — local (fastest for dev):**

```sh
cd ../scraper
uv run uvicorn scraper.app:app --host 127.0.0.1 --port 8765 --reload
```

**Option B — Docker (matches prod image):**

```sh
docker compose --profile extractor up -d extractor
```

**Verify it's healthy:**

```sh
curl -s http://localhost:8765/healthz | jq .
# → {"status":"ok"}
```

---

## 3. Smoke-Test the Service API Directly

Before running the full scrape, you can verify each API endpoint individually using the pre-generated test PDFs in the `scraper` repository.

### 3.1 Check Backends
Query the registered OCR/structuring backends:
```sh
curl -s http://localhost:8765/v1/models | jq .
```

### 3.2 Step 1 — Inspect PDF
Submit a PDF to determine page count and routing decisions (whether pages have a readable text layer or require OCR):
```sh
# Inspect a digital PDF (should route to "text")
curl -s -X POST http://localhost:8765/v1/documents:inspect \
  -H "Content-Type: application/pdf" \
  --data-binary @../scraper/tests/fixtures/digital_menu.pdf | jq .

# Inspect a scanned PDF (should route to "ocr")
curl -s -X POST http://localhost:8765/v1/documents:inspect \
  -H "Content-Type: application/pdf" \
  --data-binary @../scraper/tests/fixtures/scanned_menu.pdf | jq .
```

### 3.3 Step 2 — Extract Page Content
Extract raw text or OCR a single page (the route accepts any arbitrary document ID string since the service is stateless, and page numbers are 1-indexed):
```sh
# Extract text-layer page (using digital_menu.pdf)
curl -s -X POST http://localhost:8765/v1/documents/doc1/pages/1:extract \
  -H "Content-Type: application/pdf" \
  --data-binary @../scraper/tests/fixtures/digital_menu.pdf | jq .

# Extract OCR page (using scanned_menu.pdf)
# Note: On macOS, this triggers in-process MLX OCR. The first run takes 15–30s to load the model.
curl -s -X POST http://localhost:8765/v1/documents/doc1/pages/1:extract \
  -H "Content-Type: application/pdf" \
  --data-binary @../scraper/tests/fixtures/scanned_menu.pdf | jq .
```

### 3.4 Step 3 — Structure Merged Text
Structure a merged text layout block into a schema-validated menu document.
```sh
curl -s -X POST http://localhost:8765/v1/extractions:structure \
  -H "Content-Type: application/json" \
  -d '{"merged_text": "Mains\nMargherita Pizza $14.50 (Tomato sauce, fresh mozzarella, basil)\nPepperoni Pizza $16.00"}' | jq .
```
*(If this call times out, ensure your local Ollama server is running and the `qwen3.6:35b-mlx` model has finished loading, or set `GEMINI_API_KEY` to test via the Gemini external API.)*

---

## 4. Run the Full Scrape — Python Service Path

This is the primary Phase 2 path: scanned PDF → Python service → structured
result → Postgres, with the raw JSON payload stored losslessly.

```sh
POSTGRES_DSN="postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable"

go run . scrape "https://example-restaurant.com/scanned-menu.pdf" \
  --store postgres \
  --postgres-dsn "$POSTGRES_DSN" \
  --extractor-url http://localhost:8765 \
  --ollama-url http://localhost:11434 \
  --ollama-model nomic-embed-text
```

Expected output:
```
Scraped N menu items from "Restaurant Name" (https://...)
```

**What to check:**
- `--extractor-url` set → vision is auto-active (no `--enable-vision` needed)
- LLM extraction (`ex.Extract`) is skipped; the Python service provides structured data directly
- Each row in `restaurant_menu` has a non-null `payload` column (the raw service JSON)

---

## 5. Run the Full Scrape — Go LLM Vision Fallback Path

Uses the local OpenAI-compatible LLM for vision (no Python service required).
Requires a vision-capable model (e.g. `qwen3.6:35b-mlx` via Ollama).

```sh
go run . scrape "https://example-restaurant.com/scanned-menu.pdf" \
  --store postgres \
  --postgres-dsn "$POSTGRES_DSN" \
  --enable-vision \
  --llm-url http://localhost:11434/v1 \
  --llm-model qwen3.6:35b-mlx \
  --ollama-url http://localhost:11434 \
  --ollama-model nomic-embed-text
```

Note: `payload` will be **null** for this path — the Go LLM path produces a
`MenuExtractionResult` struct only, with no raw service JSON.

---

## 6. Run the Full Scrape — Text-Layer PDF Path

For PDFs that have a readable text layer, no vision flag is needed:

```sh
go run . scrape "https://example-restaurant.com/text-menu.pdf" \
  --store postgres \
  --postgres-dsn "$POSTGRES_DSN" \
  --llm-url http://localhost:11434/v1 \
  --llm-model qwen3.6:35b-mlx \
  --ollama-url http://localhost:11434 \
  --ollama-model nomic-embed-text
```

Use `--pdftotext` to prefer system `pdftotext` (poppler) over the built-in extractor.

---

## 7. Verify Results in Postgres

```sh
psql "postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable"
```

```sql
-- Count rows scraped
SELECT COUNT(*) FROM restaurant_menu;

-- Inspect a few items
SELECT restaurant_name, dish_name, city, state, scraped_at
FROM restaurant_menu
ORDER BY scraped_at DESC
LIMIT 10;

-- Check payload column (non-null = came from Python service path)
SELECT dish_name,
       payload IS NOT NULL AS has_payload,
       pg_column_size(payload) AS payload_bytes
FROM restaurant_menu
ORDER BY scraped_at DESC
LIMIT 5;

-- Inspect the raw payload for one item (pretty-print)
SELECT jsonb_pretty(payload)
FROM restaurant_menu
WHERE payload IS NOT NULL
LIMIT 1;

-- Confirm embedding dimension
SELECT menu_item_id, array_length(embedding::real[], 1) AS dims
FROM restaurant_menu
LIMIT 3;
-- Expected: dims = 768
```

---

## 8. Test Nearest-Neighbour Search

The `SearchMenu` method uses cosine distance on the `vector(768)` HNSW index.
There is no CLI command for menu search yet; test via `psql`:

```sql
-- Embed a query string externally (e.g. with Ollama) then paste the vector:
-- SELECT * FROM restaurant_menu ORDER BY embedding <=> '[0.1, 0.2, ...]'::vector LIMIT 5;

-- Smoke-test: the index exists and a full scan returns rows ordered by distance
EXPLAIN (ANALYZE, BUFFERS)
SELECT dish_name
FROM restaurant_menu
ORDER BY embedding <=> (SELECT embedding FROM restaurant_menu LIMIT 1)
LIMIT 5;
-- Should show "Index Scan using idx_restaurant_menu_embedding"
```

---

## 9. Troubleshooting

### "PDF has no usable text layer; set --enable-vision or --extractor-url"
The PDF is image-only. Add `--extractor-url http://localhost:8765` (Python
service) or `--enable-vision` (Go LLM fallback).

### Python service returns non-2xx on `:inspect`
Check the service logs. Common causes:
- PDF too large (default `SCRAPER_MAX_REQUEST_BODY_BYTES=20971520` = 20 MB)
- PyMuPDF failed to open the file (corrupt PDF)

### `payload` is null after `--extractor-url` scrape
The Python service returned an error or the `:structure` response had no items.
Check the Go scrape output for `"vision extraction:"` errors.

### Embedding dimension mismatch
If `dims != 768`, your Ollama model doesn't produce 768-dim vectors. The
migration creates `vector(768)` — switch to `nomic-embed-text` or update the
migration and index.

### "unexpected status 404 from .../v1/documents:inspect"
The service is running but the v1 router isn't mounted. Check
`src/scraper/app.py` — the v1 sub-app should be included under `/v1`.
