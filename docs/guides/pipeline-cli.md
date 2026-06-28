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

Alternatively, if you are running services manually, ensure the background worker is enabled:
```sh
go run . serve --enable-pipeline --postgres-dsn "postgres://user:pass@localhost:5432/fodmap"
```

## Managing Restaurants

The `restaurants` subcommand provides administrative control over the ingestion process.

### Importing Restaurants (Pagination)

Import a batch of restaurants from the NYC OpenData API (DOHMH restaurant inspection data). By default, this enqueues discovery jobs for each imported restaurant.

You can use the `--limit` and `--offset` flags to paginate through the dataset. This is extremely useful for running new batches of restaurants (e.g., 20 new restaurants) without re-importing the same ones.

```sh
# Import the first 10 restaurants in the Astoria-LIC area
go run . restaurants import --area astoria-lic --limit 10 --offset 0

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
