# Pipeline CLI Guide

The FODMAP Detector includes a robust CLI for interacting with the restaurant ingestion, discovery, and scraping pipeline.

## Running the End-to-End Pipeline

To successfully process restaurants through the entire discovery and extraction pipeline, you must run both the Go HTTP/worker server and the Python OCR scraper service.

### 1. Configure the Extractor
Ensure that your `service.yaml` routes all menu extraction to the Python scraper service:
```yaml
extractor-url: "http://localhost:8765"
discovery-max-no-url-attempts: 3   # stop retrying discovery after this many no-URL results
```
All LLM configuration (model, API key) lives in the Python service — the Go server does not pass or know about them. If the Python service is unreachable, extraction fails immediately (no fallback).

### 2. Start the Services
The easiest way to start the pipeline workers alongside the Python OCR service is using the `start.sh` script:
```sh
START_SCRAPER=true ./start.sh
```
This script handles starting Docker dependencies (PostgreSQL, Weaviate), migrating databases, launching the Python OCR service on port 8765, and starting the Go server with `--enable-pipeline`.

Alternatively, if you are running services manually:

**Python scraper service** — the webagent (headless-browser) path must be enabled, otherwise blocked sites (403/429) and JS-rendered directory pages cannot be rendered and will silently fail:
```sh
# from the scraper repo
SCRAPER_WEBAGENT_ENABLED=true \
WEBAGENT_MAX_FETCH_CONCURRENCY=4 \
uv run uvicorn scraper.app:app --port 8765
```
- `SCRAPER_WEBAGENT_ENABLED=true` — **required** for the anti-block rendered-fetch fallback and for directory/paginated-menu fanout on JS sites. Without it `POST /fetch` returns 503 and those paths fail.
- `WEBAGENT_MAX_FETCH_CONCURRENCY` — number of persistent headless browsers in the pool (default 4). It also caps the Go-side directory-fanout concurrency. Each worker is a full browser, so size it to available RAM/CPU.
- Optional escalation: `uv add camoufox` installs the heavier anti-bot tier; enable it with `WEBAGENT_USE_CAMOUFOX=true` (opt-in — installing the package alone does not change the backend). Leave it off for the default Chromium backend.

**Go server** — enable the worker and point it at the extractor:
```sh
go run . serve --enable-pipeline \
  --postgres-dsn "postgres://user:pass@localhost:5432/fodmap" \
  --extractor-url http://localhost:8765 \
  --enable-vision \
  --webagent-adapter <site/target>   # optional; see below
```
- `--extractor-url` is what wires up the webagent. With it set, the server's extractor implements the rendered-fetch fallback: blocked pages (403/429) and directory pages on JS sites are rendered via the Python webagent's `POST /fetch` automatically — **no extra Go flag needed**. (The webagent must be enabled on the Python side; see above.)
- `--webagent-adapter <site/target>` is **optional** and routes empty/too-noisy HTML to the per-site `ScrapeJS` path instead. It must name a registered adapter; omit it to rely on the generic rendered-fetch fallback, which is sufficient for the directory-fanout and 403/429 paths.

### 3. Directory / paginated menus
When a discovered URL is a **directory** (it links out to sub-menus or PDFs rather than listing items), the scrape job extracts 0 items at the root, then automatically discovers same-domain sub-URLs, validates them, fetches each through the full cascade, and aggregates the items into a single result. See [`docs/plans/paginated-menu-handling.md`](../plans/paginated-menu-handling.md). To confirm it ran, look for `extraction_tier = directory_fanout`:
```sh
go run . restaurants list --status scraped   # then inspect tier, or query Postgres:
# SELECT camis, dba, item_count, extraction_tier FROM restaurants WHERE extraction_tier = 'directory_fanout';
```

## Managing Restaurants

The `restaurants` subcommand provides administrative control over the ingestion process.

### Importing Restaurants (Pagination)

Import a batch of restaurants from the NYC OpenData API (DOHMH restaurant inspection data). By default, this enqueues discovery jobs for each imported restaurant.

You can use the `--limit` and `--offset` flags to paginate through the dataset. This is extremely useful for running new batches of restaurants (e.g., 20 new restaurants) without re-importing the same ones.

```sh
# Import the first 10 restaurants in the Astoria-LIC area
go run . restaurants --postgres-dsn "postgres://fodmap:fodmap@localhost:5432/fodmap" import --area astoria-lic --limit 10 --offset 0

# Import the next 20 new restaurants in the Astoria-LIC area
go run . restaurants import --area astoria-lic --limit 20 --offset 10
```

### Listing Restaurants

List the restaurants currently stored in the PostgreSQL `restaurants` table. You can filter by their current pipeline status or use pagination.

```sh
# List restaurants that are waiting for discovery
go run . restaurants list --status pending_discovery

# List restaurants that are waiting for scraping, offset by 50
go run . restaurants list --status pending_scrape --limit 100 --offset 50
```

### Manual Triggering

You can manually force a specific restaurant (by its CAMIS ID) into a specific stage of the pipeline.

```sh
# Enqueue a discovery job (searches for the restaurant's website and menu URLs)
go run . restaurants discover 50012345

# Enqueue a scrape job (extracts the menu items and embeddings from the discovered URLs)
go run . restaurants scrape 50012345
```

### Error Recovery

If a restaurant fails at any point in the pipeline (e.g., website 404, LLM extraction error, connection timeout), you can reset its status and requeue it.

```sh
go run . restaurants retry 50012345
```
  
```sh
export POSTGRES_DSN="postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable"
go run . restaurants retry-all-failed
```

### Replaying Restaurants from the Bronze Layer

The `replay-restaurants` subcommand re-runs the restaurant upsert and discovery-enqueue flow from NYC restaurant records already persisted in the Avro bronze layer — it skips the NYC OpenData API fetch entirely. This is useful for re-driving a pipeline run from saved data (e.g., after a DB wipe or to re-queue discovery jobs).

`--limit` and `--offset` apply **globally across all loaded Avro records**, not per file.

```sh
export POSTGRES_DSN="postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable"

# Replay every .avro file under the bronze directory (default: data/bronze/restaurants)
go run . restaurants replay-restaurants

# Replay a single specific avro file
go run . restaurants replay-restaurants --avro-file data/bronze/restaurants/astoria-lic-2026-06-29.avro

# Replay only records 20..39 (across all files), and skip enqueuing discovery jobs
go run . restaurants replay-restaurants --limit 20 --offset 20 --skip-discovery
```

Flags:
- `--avro-file <path>` — replay a single file; when omitted, the bronze dir is scanned for `*.avro`.
- `--bronze-dir data/bronze/restaurants` — directory to scan when `--avro-file` is unset.
- `--postgres-dsn` — PostgreSQL DSN (or `POSTGRES_DSN` env var).
- `--limit N` — cap the number of records replayed across all files (0 = all).
- `--offset N` — skip the first N records across all files.
- `--skip-discovery` — only upsert restaurants; don't enqueue `DiscoverMenuURL` jobs.
