# Python Extraction Service Plan (`extractor-py`) — ARCHIVED

> ## ⚠ ARCHIVED — historical reasoning record only
>
> **This plan was never built.** It is kept solely so future maintainers can see what alternative was considered and why it was dropped. **Do not implement what is described below — it is no longer the project's direction.**
>
> ### Why it was dropped
>
> The plan proposed a stateless Python microservice (FastAPI + `trafilatura` + `pymupdf` + `pytesseract` + `playwright`) to handle the four hardest extraction problems the Go pipeline couldn't.
>
> Two later findings made it unnecessary:
>
> 1. **Pure-Go libraries fill most gaps.** `github.com/chromedp/chromedp` covers JS rendering. `github.com/markusmobius/go-trafilatura` (Go port of the Python original) covers boilerplate removal. Both proved adequate for restaurant menus.
> 2. **A unified vision-LLM handles PDFs and image menus.** Qwen3-VL (served via Ollama on Mac as `qwen3.6:35b-mlx`, or via vLLM on Linux as `Qwen/Qwen3-VL-8B-Instruct-AWQ`) reads PDFs/images directly through the standard OpenAI Chat Completions `image_url` content-part wire format — the same endpoint already used for text menu extraction. No separate Python OCR service is needed.
>
> The cost of the Python service (second deployment artifact, cross-language IPC, second CI workflow, dual maintenance) was not worth its diminishing benefit once the Go alternatives matured.
>
> ### When this plan might be revived
>
> - If `chromedp` or `go-trafilatura` quality on real menus turns out to be materially worse than the Python equivalents
> - If a benchmark-specialized OCR model (e.g. Datalab Chandra) becomes important AND we have ≥24 GB GPU headroom to run it alongside the menu-extraction LLM
> - If the project takes on non-restaurant document-extraction work that benefits from `pymupdf` / `pdfplumber` / `pytesseract` quality
>
> For the current plan, see [scraper-pipeline-plan.md](scraper-pipeline-plan.md) and [llm-serving.md](llm-serving.md).

---

> **Everything below this line is the original (un-edited) plan from when this was the proposed direction.** All present-tense statements ("we add", "the service does") describe what the plan *would have done* if built — not what is being built today.

## Context

The Go scraper handles ~70% of restaurant sites cleanly (server-rendered HTML, simple PDFs). But four classes of pages defeat pure-Go tooling:

1. **JS-only SPAs** (Toast, Squarespace, Resy) — no content in initial HTML
2. **Complex multi-column or scanned PDFs** — pure-Go libraries can't extract them
3. **Boilerplate-heavy pages** — main content buried in nav/ads/related-posts
4. **Image-based menus** (PDFs that are just photos of paper menus)

Python's ecosystem is years ahead for all four. We add a small stateless Python microservice that the Go CLI calls only when needed.

## Service Boundary

Stateless HTTP service. **No state, no database, no LLM calls.** Input: URL or raw bytes. Output: clean Markdown/text. The Go side keeps owning the LLM call, embedding, and storage.

```
POST /extract
  body: { "url": "...", "render_js": false, "force_ocr": false }
  → 200 { "format": "markdown" | "text", "content": "...",
          "title": "...", "warnings": ["..."] }

POST /extract/bytes
  body: multipart upload (PDF, HTML, image)
  Content-Type drives the extractor choice
  → 200 same shape as above
```

Errors are JSON: `{ "error": "fetch_failed" | "render_timeout" | "unsupported" | "robots_disallow", "detail": "..." }`.

## Architecture (Tier mapping)

| Tier | Where | Tool |
|---|---|---|
| 0 | Go (existing) | JSON-LD fast-path |
| 1 | Go (existing) | Plain HTTP + `golang.org/x/net/html` |
| 1-py | Python | `trafilatura` (boilerplate removal) for tricky HTML where Tier 1 returns garbage |
| 2 | Go (existing) | LLM API-endpoint inference (experimental) |
| 3 | Python | `playwright` headless Chromium for JS-only SPAs |
| PDF-py | Python | `pymupdf` for complex PDFs; falls back to `pdfplumber` then `pytesseract` (OCR) |

The Go CLI calls Python when:
- Tier 1 returns < 3 menu items AND `--use-python-extractor` flag is set
- Content-Type is `application/pdf` AND `--use-python-pdf` is set
- `--force-js-render` is set (skip Tiers 0–2, go straight to Tier 3)

## Tech Stack

| Concern | Library | Why |
|---|---|---|
| HTTP framework | **FastAPI** | Pydantic-validated schemas, OpenAPI docs free, async I/O for concurrent renders |
| HTML→Markdown | **trafilatura** | Built for content extraction (strips nav/ads/comments). Far better than markdownify for restaurant sites. |
| PDF (text) | **pymupdf** (`fitz`) | Best pure-Python PDF text extractor. Handles columns, tables, fonts. |
| PDF (OCR fallback) | **pytesseract** + **pdf2image** | When PDF has no text layer (scanned menus) |
| JS render | **playwright** | More reliable than Selenium, better than Puppeteer for Python |
| Charset detect | **chardet** (transitive via `trafilatura`) | Robust for non-UTF-8 menus |
| Tests | **pytest** + **httpx.AsyncClient** | Async test client matches FastAPI's async endpoints |
| Linting | **ruff** + **mypy --strict** | Match the quality bar of the Go side |

## Repo Layout

Lives in the same repo under `extractor-py/` (monorepo) so a single PR can update both sides atomically:

```
extractor-py/
├── pyproject.toml           # uv or poetry — pin Python 3.12
├── README.md                # how to run locally + Dockerfile usage
├── Dockerfile               # multi-stage, installs Chromium for Playwright
├── extractor/
│   ├── __init__.py
│   ├── main.py              # FastAPI app
│   ├── settings.py          # Pydantic Settings (env-driven)
│   ├── fetcher.py           # httpx fetch w/ robots.txt + max bytes + UA
│   ├── html_extractor.py    # trafilatura wrapper
│   ├── pdf_extractor.py     # pymupdf → pdfplumber → OCR cascade
│   ├── js_renderer.py       # playwright wrapper (singleton browser)
│   └── routes.py            # POST /extract, POST /extract/bytes, GET /healthz
└── tests/
    ├── conftest.py          # FastAPI TestClient + Playwright fixture
    ├── test_html.py
    ├── test_pdf.py
    ├── test_js_render.py    # marked @pytest.mark.slow, gated in CI
    └── fixtures/
        ├── multi_column_menu.pdf
        ├── scanned_menu.pdf
        └── spa_snapshot.html
```

## Key Design Choices

- **Stateless, no DB** — restarts are free; horizontal scaling is trivial.
- **Playwright browser is a singleton** — launch once on app startup, reuse across requests. Lazy-initialized so PDF-only deployments don't pay the Chromium download cost.
- **`/extract` accepts a URL** rather than requiring the Go side to download bytes first, so JS rendering can drive the fetch directly (cookies, headers, redirects all stay inside the browser).
- **Hard limits**: 30s default request timeout, 20MB max body, 1 browser context per request, browser context killed if it survives longer than the timeout.
- **No LLM client in this service.** Keep the boundary clean. Adding LLM here later would create two places that talk to Ollama/OpenAI.
- **Healthcheck**: `GET /healthz` returns Playwright browser status + memory.

## Go ↔ Python Integration

New Go file `scraper/python_extractor.go` implements `scraper.Extractor` (same interface as the Go-native extractors) by POSTing to the Python service. From the Go pipeline's perspective, it's just another `Extractor` implementation — no other Go code changes.

New CLI flags on `fodmap scrape`:
```
--python-extractor-url     URL of extractor-py service (default: unset, disables Python path)
--use-python-extractor     Try Python fallback when Tier 1 returns trivial result
--use-python-pdf           Send PDFs to Python instead of ledongthuc/pdf
--force-js-render          Skip Tiers 0/1/2 entirely, go straight to JS render
```

If `--python-extractor-url` is unset, all Python flags are no-ops and the existing Go behavior is unchanged.

## Deployment

- Docker image: `extractor-py:latest`, multi-stage build, ~1.2GB (Chromium is heavy)
- Update `docker-compose.yaml`: add `extractor-py` service, expose on internal network only
- Update `start.sh` to launch the Python service alongside the Go server (per CLAUDE.md: `start.sh` must stay in working order)
- Health probe: `GET /healthz`, readiness probe waits until first Playwright launch succeeds
- Resource hints: 1 vCPU, 2 GB RAM per replica; Chromium is the dominant consumer

## Testing

- `test_html.py`: trafilatura on a fixture HTML page → assert menu sections kept, nav stripped
- `test_pdf.py`: pymupdf on multi-column PDF → assert correct column reading order; OCR test on scanned PDF (marked `@pytest.mark.ocr`, skipped if tesseract not installed)
- `test_js_render.py`: Playwright renders a fixture SPA served by `httpx` → assert visible content extracted (marked `@pytest.mark.slow`)
- Contract test on the Go side: `scraper/python_extractor_test.go` stubs the Python service with `httptest.NewServer` returning canned `/extract` responses → assert Go side correctly parses them
- CI: a GitHub Action workflow `extractor-py.yml` runs ruff + mypy + pytest. The Go workflow stays unchanged (the Python service is optional).

## Known Limitations / Out of Scope

- **No batch endpoint** in v1. Add `POST /extract/batch` later if call patterns demand it.
- **No persistent cache** — repeated scrapes of the same URL re-fetch. The Go side handles dedup via deterministic IDs.
- **OCR quality** depends on Tesseract version + language packs. Default English only.
- **No anti-bot evasion** — no proxies, no stealth plugins, no CAPTCHA solving. If a site blocks us, we fail with `bot_blocked`.
- **Memory pressure** — long-running Playwright contexts leak; we kill on timeout and restart the browser process every N requests (configurable).

## Related Plans

- [scraper-pipeline-plan.md](scraper-pipeline-plan.md) — the parent Go pipeline plan that this service plugs into
- [llm-serving.md](llm-serving.md) — local LLM serving choices

## Verification

```bash
# Local dev
cd extractor-py
uv sync                                # or: poetry install
uv run playwright install chromium
uv run uvicorn extractor.main:app --reload --port 8765

# Smoke test the service directly
curl -X POST http://localhost:8765/extract \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","render_js":false}'

# End-to-end through Go
PYTHON_EXTRACTOR_URL=http://localhost:8765 \
  go run . scrape "https://some-toast-takeout-site.com/menu" \
  --use-python-extractor --force-js-render
```
