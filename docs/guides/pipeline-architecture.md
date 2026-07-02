# Pipeline Architecture

The menu pipeline is split across two services:

## Go Service (`fodmap-detector`)
Responsible for orchestration, fetching standard content, and database interactions:
- **Discovery** — Gemini API call to find menu URLs for each restaurant, plus a deterministic harvest of ordering-platform links (`dine.online`, Toast, `order.store`, …) from the primary website's HTML — Gemini frequently misses the ordering SPA the homepage links to, and those carry the only complete menu for many restaurants.
- **Fetching** — HTTP GET of plain HTML and PDF menu pages.
- **JSON-LD extraction (Tier 0)** — structured menu data embedded in the page is extracted directly in Go, skipping the Python service entirely.
- **Job orchestration** — River queue, retries, status tracking in Postgres.
- **Storage** — upserts menu items into Weaviate / pgvector.

## Python Service (`../scraper`)
Running on `:8765`, responsible for AI structuring and heavy JS rendering:
- **Fetching JS-rendered pages** — headless browser via the webagent (Playwright); Go never sees the raw HTML for these.
- **Parsing HTML/text menus** — page text is sent to `/v1/extractions:structure`, which calls the service's own LLM (configured independently, defaults to Gemini).
- **PDF menus** — inspect page count → OCR each page → structure.
- **Image menus** — OCR the image → structure.

*The Python service owns all LLM configuration for parsing. The Go service only invokes it via its REST API and stores the results.*

---

## Retry Cost Controls

- **Discovery worker**: if a retry finds URLs already stored in Postgres (Gemini succeeded but the subsequent write/enqueue failed), it skips the Gemini call and goes straight to re-enqueueing scrape jobs. Configurable via `discovery-max-no-url-attempts` (default 3) to stop retrying when no URL is ever found.
- **Python structuring**: an in-process LRU cache (`SCRAPER_STRUCTURING_CACHE_SIZE`, default 512) returns cached results for repeated inputs, avoiding redundant LLM calls on River retries.

---

## See also

- [data-model.md](data-model.md) — core data structures and flowchart of the fallback tiers.
- [scrape-diagnostics.md](scrape-diagnostics.md) — operational runbook for tracing a scrape from outcome back to root cause (status/tier queries, bronze/silver inspection, cascade logs, cross-service `request_id` correlation).
