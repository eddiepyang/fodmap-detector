# Troubleshooting Guide

This guide covers common issues, diagnostic procedures, and migration instructions for the FODMAP Detector application.

---

## 1. Stale Weaviate Schema Migrations

### The Problem
During development, if a property type changes in the Go data structures (for example, the `substitutions` field on `FodmapIngredient` changing from a single `text` string to a `text[]` string array), Weaviate will not dynamically update the schema of an existing class. On startup, the server will fail to load or batch upsert entries and write warning/error logs such as:

```
invalid text property 'substitutions' on class 'FodmapIngredient': not a string, but []interface {}
```

Similarly, if search is returning empty results (`{"businesses":[]}`) for every query (even general keywords like `"beer"` or `"pizza"`), the cross-references between reviews and chunks may be corrupted or using an obsolete format.

> [!IMPORTANT]
> **This schema migration must be performed manually on all running instances of the application** (local development machines, staging environments, production deployments, etc.) when deploying updates that modify the database structures.

### Solution: Dropping Obsolete Schema Classes
To resolve type mismatches, you must drop the stale Weaviate classes and let the Go backend recreate them cleanly on the next startup:

```bash
# Delete the FodmapIngredient schema (recreated automatically on serve startup)
curl -X DELETE http://localhost:8090/v1/schema/FodmapIngredient

# Delete the YelpReview schemas (requires running the index command again)
curl -X DELETE http://localhost:8090/v1/schema/YelpReviewChunk
curl -X DELETE http://localhost:8090/v1/schema/YelpReview
```

---

## 2. Querying Weaviate Directly for Diagnostics

If you need to verify that records are correctly indexed and reference linkage is intact, you can query Weaviate directly using standard curl requests.

### Check Existing Schema Properties
Inspect properties and types currently registered in Weaviate:
```bash
curl -s http://localhost:8090/v1/schema
```

### Fetch Objects via REST API
Fetch sample records directly from specific classes:
```bash
# Fetch a YelpReview
curl -s "http://localhost:8090/v1/objects?class=YelpReview&limit=1"

# Fetch a YelpReviewChunk
curl -s "http://localhost:8090/v1/objects?class=YelpReviewChunk&limit=1"
```

### Validate Reference Linkage via GraphQL
Execute a GraphQL query to ensure that `YelpReviewChunk` is successfully linked back to its parent `YelpReview` record:
```bash
curl -s -X POST \
  -H "Content-Type: application/json" \
  -d '{"query": "{ Get { YelpReviewChunk (limit: 2) { chunkText hasParent { ... on YelpReview { reviewId businessId businessName } } } } }"}' \
  http://localhost:8090/v1/graphql
```

#### Successful Response Format
For single-target references, Weaviate flat-maps the properties inside the `hasParent` array:
```json
{
  "data": {
    "Get": {
      "YelpReviewChunk": [
        {
          "chunkText": "Good selection of your Thai favorites...",
          "hasParent": [
            {
              "businessId": "tmYa9OC8NE4ov2BoLyL2WQ",
              "businessName": "Thai Island",
              "reviewId": "z0acnaJ9GKC7-cElSspbNg"
            }
          ]
        }
      ]
    }
  }
}
```

---

## 3. Port Conflicts (`bind: address already in use`)

### The Problem
If the backend or frontend fails to start with errors such as:
```
server error: listen tcp :8081: bind: address already in use
```
An existing instance of the server is already running in the background.

### Solution
1. Find the PID of the process using the port:
   ```bash
   # For the backend (8081)
   lsof -i :8081

   # For the frontend (5173)
   lsof -i :5173
   ```
2. Terminate the process:
   ```bash
   kill <PID>
   # Or forcefully if it resists
   kill -9 <PID>
   ```
3. Restart using `make start` or `make run`.

---

## 4. Chat Streaming: Corrupted Thought Signature (400)

### The Problem
During a chat stream, the application may fail with an "unknown streaming error", and the server logs will show:

```
model generation error: stream error: Error 400, Message: Corrupted thought signature., Status: INVALID_ARGUMENT
```

### The Cause
Gemini models (particularly reasoning models like `gemini-3-flash-preview`) output a binary **thought signature** block when performing internal thinking or function calling. When passing this history back to Gemini in subsequent turns, the exact binary tokens must be provided. 

Previously, the backend converted the raw binary bytes of the signature directly to a Go `string` (`string(part.ThoughtSignature)`). Since Go strings expect valid UTF-8 sequences, any arbitrary binary bytes that did not conform to UTF-8 were corrupted/replaced. When the backend cast this string back to a byte slice (`[]byte`) to send to Gemini, the model rejected it as a corrupted thought signature.

### The Solution
We updated the `chat` package to safely encode the `ThoughtSignature` into a **base64 string** before serialization, and decode it back to the exact original binary bytes when sending the conversation history to the API:

- **Serialization**: `base64.StdEncoding.EncodeToString(part.ThoughtSignature)`
- **Deserialization**: `base64.StdEncoding.DecodeString(fc.ThoughtSignature)`

If you are developing custom clients or wrappers that manage Gemini's reasoning history, ensure you serialize binary thought signatures as base64 strings rather than converting them directly to raw UTF-8 strings.

---

## 5. Admin & User Account Diagnostics (PostgreSQL)

During testing, you may encounter issues registering admin/user accounts (e.g. duplicate key errors) or need to manually check user roles.

### Diagnosing Duplicate Registrations
If registration fails with the following error:
```
failed to create user: ERROR: duplicate key value violates unique constraint "users_email_key" (SQLSTATE 23505)
```
The user is already registered in the PostgreSQL database.

### Direct Database Queries via Docker
Since PostgreSQL runs inside a Docker container (`fodmap-detector-postgres-1`), you can run `psql` queries directly from your shell:

#### 1. Search for a Specific User Record
```bash
docker exec -it fodmap-detector-postgres-1 psql -U fodmap -d fodmap -c "SELECT id, email, role, status, created_at FROM users WHERE email = 'admin@example.com';"
```

#### 2. List All Registered Users
```bash
docker exec -it fodmap-detector-postgres-1 psql -U fodmap -d fodmap -c "SELECT id, email, role, status FROM users;"
```

#### 3. Delete/Reset an Account (for Fresh Registration)
To delete a user account so you can register it fresh through the UI:
```bash
docker exec -it fodmap-detector-postgres-1 psql -U fodmap -d fodmap -c "DELETE FROM users WHERE email = 'admin@example.com';"
```

#### 4. Manually Promote a User to Admin
If you need to manually grant a registered user admin privileges:
```bash
docker exec -it fodmap-detector-postgres-1 psql -U fodmap -d fodmap -c "UPDATE users SET role = 'admin' WHERE email = 'user@example.com';"
```

---

## 6. Postgres Migration Issues

Domain table DDL is managed by `golang-migrate` and applied via `go run . db migrate-up`. The migration state is tracked in the `schema_migrations` table.

### Check current migration version
```bash
go run . db migrate-version
```

### Migrations fail on existing database
If `db migrate-up` fails on a database that already has tables (e.g. from before the migration system was introduced), the baseline migration uses `IF NOT EXISTS` and should be safe. If you see errors about duplicate objects, you can force-mark the baseline as applied:
```bash
go run . db migrate-force 1
```
Then run `db migrate-up` again to apply any remaining migrations.

### Reset migrations (destructive)
To wipe the migration state and start fresh (only for development):
```bash
go run . db migrate-down
```
This drops all domain tables. You can then run `db migrate-up` to recreate them.

### River's own tables
River's schema tables (`river_job`, `river_leader`, `river_queue`, `river_client`) are managed separately by `river migrate-up` and are **not** included in `golang-migrate` migrations. Do not attempt to manage them with `db migrate-up/down`.

---

## 7. River Job Queue Diagnostics

The menu pipeline (discovery + scraping) runs as River jobs inside PostgreSQL. All diagnostic queries below use the `river_job` table.

### River job states

| State | Meaning |
|---|---|
| `available` | Queued and ready for a worker to pick up |
| `running` | Currently being processed by a worker |
| `scheduled` | Waiting until `scheduled_at` (e.g. staggered scrape jobs) |
| `retryable` | Failed; will be retried after `scheduled_at` |
| `discarded` | Exhausted all attempts; written to `menutracking_dead_letter` |
| `cancelled` | Manually cancelled |
| `completed` | Finished successfully |

### Overview: job counts by kind and state

```bash
docker compose exec postgres psql -U fodmap -d fodmap -c \
  "SELECT kind, state, count(*) FROM river_job GROUP BY 1, 2 ORDER BY 1, 2;"
```

Key job kinds in the pipeline:
- `menusearch.discover_menu_url` — Gemini web-search for a restaurant's menu URLs
- `menusearch.scrape_menu` — fetch + extract a single menu URL via the Python scraper

### Check errors on failing jobs

```bash
# Most recent error on retryable scrape jobs
docker compose exec postgres psql -U fodmap -d fodmap -x -c \
  "SELECT id, args->>'camis' AS camis, attempt, max_attempts, errors->-1->>'error' AS last_error
   FROM river_job
   WHERE kind = 'menusearch.scrape_menu' AND state = 'retryable'
   ORDER BY id DESC LIMIT 5;"

# Same for discovery jobs
docker compose exec postgres psql -U fodmap -d fodmap -x -c \
  "SELECT id, args->>'dba' AS dba, attempt, max_attempts, errors->-1->>'error' AS last_error
   FROM river_job
   WHERE kind = 'menusearch.discover_menu_url' AND state = 'retryable'
   ORDER BY id DESC LIMIT 5;"
```

### Check permanently discarded jobs

Jobs that exhaust all attempts are written to `menutracking_dead_letter`:

```bash
docker compose exec postgres psql -U fodmap -d fodmap -x -c \
  "SELECT job_kind, job_args->>'camis' AS camis, error, discarded_at
   FROM menutracking_dead_letter
   ORDER BY discarded_at DESC LIMIT 10;"
```

### Check scheduled (future) jobs

Scrape jobs are staggered by `discovery-stagger-seconds` (default 15 s per URL). Jobs in the `scheduled` state are waiting for their `scheduled_at`:

```bash
docker compose exec postgres psql -U fodmap -d fodmap -c \
  "SELECT kind, count(*), min(scheduled_at), max(scheduled_at)
   FROM river_job WHERE state = 'scheduled' GROUP BY 1;"
```

### Check restaurant pipeline status

The `restaurants` table tracks each restaurant's pipeline stage:

```bash
docker compose exec postgres psql -U fodmap -d fodmap -c \
  "SELECT status, count(*) FROM restaurants GROUP BY 1 ORDER BY 1;"
```

Common statuses: `pending_discovery`, `pending_scrape`, `scraped`, `failed_scrape`, `no_url_found`.

To see which restaurants are stuck:

```bash
docker compose exec postgres psql -U fodmap -d fodmap -c \
  "SELECT camis, dba, status, array_length(menu_urls, 1) AS url_count
   FROM restaurants WHERE status NOT IN ('scraped') ORDER BY status, dba LIMIT 20;"
```

### Common causes and fixes

**Cause A: Python scraper service not running**

If scrape jobs fail with a connection error to `localhost:8765`, the Python scraper is not running.

```bash
curl -s http://localhost:8765/healthz   # should return {"status":"ok"}
```

Start it: `cd ../scraper && ./start.sh` (or `uv run uvicorn scraper.app:app --port 8765`).

**Cause B: Scrape worker not registered**

If the error is `"job kind is not registered in the client's Workers bundle: menusearch.scrape_menu"`, the server started without an extractor URL configured. The pipeline skips registering the scrape worker when `extractor-url` is empty.

Ensure `service.yaml` has:
```yaml
extractor-url: "http://localhost:8765"
```
Then restart the server with `--enable-pipeline`.

**Cause C: Discovery jobs stopped retrying (no URL found)**

Discovery retries are capped by `discovery-max-no-url-attempts` (default 3). After that, the job returns `nil` and the restaurant is marked `no_url_found`. This is intentional — it means the restaurant has no discoverable web presence.

To re-run discovery for a specific restaurant:
```bash
go run . restaurants discover <CAMIS>
```

**Cause D: Scrape jobs staggered into the future**

This is normal: scrape jobs for multiple URLs from the same restaurant are scheduled `discovery-stagger-seconds` apart. They will run automatically. If you want them to run immediately (e.g. after fixing a bug):

```bash
docker compose exec postgres psql -U fodmap -d fodmap -c \
  "UPDATE river_job SET state='available', scheduled_at=now()
   WHERE kind='menusearch.scrape_menu' AND state='scheduled';"
```

### Resetting retryable jobs to run immediately

After fixing a configuration issue, force all retrying jobs to re-run now:

```bash
docker compose exec postgres psql -U fodmap -d fodmap -c \
  "UPDATE river_job SET state='available', attempt=0, scheduled_at=now()
   WHERE state='retryable' AND kind IN ('menusearch.discover_menu_url','menusearch.scrape_menu');"
```
