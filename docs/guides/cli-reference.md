# CLI Reference

### CLI

Run the CLI with:

```sh
go run .
```

#### Commands

For information on running the background pipeline and managing restaurants via CLI, see the [Pipeline CLI Guide](pipeline-cli.md).
For information on how to query Weaviate directly, see the [Weaviate Query Guide](weaviate-queries.md).

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

# Scrape an image-based menu or PDF using a local Vision LLM.
# Use vllm-metal (Mac) or vLLM (5080) — NOT Ollama: Ollama's MLX engine does not
# enforce response_format json_schema, so structured extraction is unreliable.
# See docs/guides/llm-serving.md for serving the model.
go run . scrape "https://example-restaurant.com/menu.pdf" \
  --weaviate localhost:8090 \
  --llm-url http://localhost:8000/v1 \
  --llm-model qwen3-vl \
  --enable-vision

# Route PDF/OCR extraction to the Python scraper service (handles vector-outline
# and scanned PDFs that pure-Go vision can't). The service must be running
# (e.g. cd ../scraper && uv run uvicorn scraper.app:app --port 8765).
go run . scrape "https://example-restaurant.com/menu.pdf" \
  --weaviate localhost:8090 \
  --llm-url http://localhost:8000/v1 \
  --extractor-url http://localhost:8765

# Scrape a page whose menu is an embedded image (e.g. a photo of a printed
# trifold menu). With --enable-vision, the detector's own tuned vision path
# detects the menu image in the HTML and OCRs it directly — no Python service
# required. (If --extractor-url is set instead, the service's two-stage
# OCR→structure path is used; prefer --enable-vision for single images — see
# docs/guides/vision-extraction.md.)
go run . scrape "https://thriftnsipcafe.com/#MENU" \
  --weaviate localhost:8090 \
  --llm-url http://localhost:8000/v1 \
  --llm-model qwen3-vl \
  --enable-vision

# Route JS-rendered pages to the detector's generic render-and-re-cascade path.
# Needs Google Chrome / Chromium installed (chromedp finds it automatically). No
# per-site adapter, no Python service — the headless browser renders the page, the
# text/image cascade re-runs on the hydrated DOM. Covers Wix/Squarespace/React
# sites whose menu hydrates client-side.
go run . scrape "https://worldspa.com/dining/" \
  --weaviate localhost:8090 \
  --llm-url http://localhost:8000/v1 \
  --llm-model qwen3-vl \
  --enable-js-render

# Route JS-rendered pages to the service's per-site webagent (Phase B). Use this
# for interaction-heavy sites (click-to-reveal menu, infinite scroll, auth) where
# a generic render is not enough. Requires a pre-compiled adapter (see
# ../scraper/src/scraper/webagent/discovery/cli.py).
go run . scrape "https://js-heavy-site.com/menu" \
  --weaviate localhost:8090 \
  --llm-url http://localhost:8000/v1 \
  --extractor-url http://localhost:8765 \
  --enable-js-render --webagent-adapter site/target
```

| Flag | Default | Description |
|------|---------|-------------|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--llm-url` | `http://localhost:8000/v1` | Base URL for OpenAI-compatible LLM endpoint (vLLM/vllm-metal; Ollama's MLX can't enforce json_schema) |
| `--llm-model` | `qwen3-vl` | LLM model to use |
| `--enable-vision` | `false` | Use the detector's own tuned vision LLM to OCR PDFs and image-embedded menus (pure-Go; no service dependency). Normalizes any decodable image (PNG/JPEG/GIF/WEBP) to PNG before sending. Alternative to `--extractor-url` |
| `--pdftotext` | `false` | Fall back to system `pdftotext` (poppler) for PDF text extraction |
| `--extractor-url` | `""` | Base URL of the Python scraper service for PDF/OCR + image-embedded menus (e.g. `http://localhost:8765`); empty = pure-Go default |
| `--extractor-page-timeout` | `2m` | Per-page request timeout when calling the scraper service |
| `--extractor-pdf-timeout` | `10m` | Overall PDF deadline when calling the scraper service |
| `--enable-js-render` | `false` | Render JS-only pages in a headless Chrome and re-cascade on the hydrated DOM. Without `--webagent-adapter`: generic render path (needs Chrome installed, no service). With `--webagent-adapter`: per-site webagent path (needs `--extractor-url` + adapter) |
| `--webagent-adapter` | `""` | webagent adapter ID (`site/target`) for JS-rendered pages |

**Scrape fallback cascade:**

1. **Tier 0 — JSON-LD:** parse schema.org `Menu` blocks (no LLM call).
2. **Tier 1 — HTML/PDF → LLM:** HTML→Markdown or PDF text-layer → `ex.Extract`.
3. **Tier 1.5 — trafilatura:** if Markdown is noisy, try boilerplate removal.
4. **Phase C — Image-embedded menu (two routes):**
   - *Pre-text:* if content is still noisy/empty/too-short, scan HTML for a
     large `<img>` likely to be a menu photo; if found, fetch+OCR it.
   - *Post-text-empty (G1 fix):* even when the HTML has enough boilerplate to
     pass the noisy/short gate, if `ex.Extract` returns **0 items** and a
     menu-image candidate exists, OCR the image and prefer that result. This
     reaches image-only menus on text-heavy marketing pages (the common
     Wix/Squarespace case).
   - Either route uses the detector's own vision path with `--enable-vision`
     (no service needed) or the service's two-stage OCR→structure with
     `--extractor-url`. Prefer `--enable-vision` for single images; reserve
     the service for multi-page/scanned PDFs (see
     [vision-extraction.md](vision-extraction.md)). Image candidates are
     tried in score order (bounded) and gated by the "IS THIS A MENU?" prompt
     guard — a non-menu photo returns 0 items and nothing is indexed.
5. **Phase B — JS render (two routes):**
   - *Generic render-and-re-cascade* (`--enable-js-render` with no adapter): a
     headless Chrome (`ChromeRenderedFetcher`) renders the page, the text/image
     cascade re-runs on the hydrated DOM, and menu-image candidates are
     re-scanned. Covers Wix/Squarespace/React sites with no per-site authoring.
     Requires Google Chrome / Chromium installed.
   - *Per-site webagent* (`--enable-js-render` + `--webagent-adapter`): routes
     to the service's webagent endpoint. Preferred when an adapter exists — it
     has selector-level guarantees the generic render lacks. Used for
     interaction-heavy sites (click-to-reveal menu, infinite scroll, auth).
6. **PDF service path:** PDFs without a text layer route to the service (inspect → extract → structure), with pure-Go vision as the 503 fallback.

**Config ownership split:** when `--extractor-url` is set, PDF structuring is
owned by the service's `SCRAPER_LLM_*` / OCR backend config — the detector's
`--llm-model`/`--llm-url` only drive the HTML/text path (embeddings remain on
`--ollama-*`). See `docs/plans/scraper-service-integration-plan.md` for details.

##### Chat (interactive FODMAP/allergen agent)

```sh
# Find the top Thai restaurant in Las Vegas and start a chat about its dishes
GEMINI_API_KEY=${GEMINI_KEY} go run . chat "pad thai" --city "Las Vegas" --state NV
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

