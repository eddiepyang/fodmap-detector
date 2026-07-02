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
River's schema tables (`river_job`, `river_leader`, `river_queue`, `river_client`) are managed separately by `river migrate-up` and live in the `river` schema. They are **not** included in `golang-migrate` migrations. Do not attempt to manage them with `db migrate-up/down`.

> **Migrating from `public` (existing deployments):** this build moved River
> tables into the `river` schema. `db migrate-up` detects an existing
> deployment (river_job in public but not in river) and hard-errors with the
> one-time `ALTER TABLE ... SET SCHEMA river` steps. Run them, then re-run
> `db migrate-up`. See [docs/plans/river-schema-and-dual-write-plan.md](../plans/river-schema-and-dual-write-plan.md).

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
  "SELECT kind, state, count(*) FROM river.river_job GROUP BY 1, 2 ORDER BY 1, 2;"
```

Key job kinds in the pipeline:
- `menusearch.discover_menu_url` — Gemini web-search for a restaurant's menu URLs
- `menusearch.scrape_menu` — fetch + extract a single menu URL via the Python scraper

### Check errors on failing jobs

```bash
# Most recent error on retryable scrape jobs
docker compose exec postgres psql -U fodmap -d fodmap -x -c \
  "SELECT id, args->>'camis' AS camis, attempt, max_attempts, errors->-1->>'error' AS last_error
   FROM river.river_job
   WHERE kind = 'menusearch.scrape_menu' AND state = 'retryable'
   ORDER BY id DESC LIMIT 5;"

# Same for discovery jobs
docker compose exec postgres psql -U fodmap -d fodmap -x -c \
  "SELECT id, args->>'dba' AS dba, attempt, max_attempts, errors->-1->>'error' AS last_error
   FROM river.river_job
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
   FROM river.river_job WHERE state = 'scheduled' GROUP BY 1;"
```

### Check restaurant pipeline status

The `restaurants` table tracks each restaurant's pipeline stage:

```bash
docker compose exec postgres psql -U fodmap -d fodmap -c \
  "SELECT status, count(*) FROM restaurants GROUP BY 1 ORDER BY 1;"
```

Common statuses: `pending_discovery`, `pending_scrape`, `scraped`, `failed_scrape`, `failed_permanently`.

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

When the scrape worker is not registered, `POST /api/v1/restaurants/{camis}/scrape` and the retry endpoint return `503` with `"scrape worker not configured"` instead of enqueuing an unhandled job. Likewise, if `GOOGLE_API_KEY` is unset, the discover worker is not registered and `POST /api/v1/restaurants/{camis}/discover` returns `503` with `"discovery worker not configured"`.

**Cause C: Discovery jobs stopped retrying (no URL found)**

Discovery retries are capped by `discovery-max-no-url-attempts` (default 3). After that, the job returns `nil` and the restaurant is marked `failed_permanently`. This is intentional — it means the restaurant has no discoverable web presence.

To re-run discovery for a specific restaurant:
```bash
go run . restaurants discover <CAMIS>
```

**Cause D: Scrape jobs staggered into the future**

This is normal: scrape jobs for multiple URLs from the same restaurant are scheduled `discovery-stagger-seconds` apart. They will run automatically. If you want them to run immediately (e.g. after fixing a bug):

```bash
docker compose exec postgres psql -U fodmap -d fodmap -c \
  "UPDATE river.river_job SET state='available', scheduled_at=now()
   WHERE kind='menusearch.scrape_menu' AND state='scheduled';"
```

### Resetting retryable jobs to run immediately

After fixing a configuration issue, force all retrying jobs to re-run now:

```bash
docker compose exec postgres psql -U fodmap -d fodmap -c \
  "UPDATE river.river_job SET state='available', attempt=0, scheduled_at=now()
   WHERE state='retryable' AND kind IN ('menusearch.discover_menu_url','menusearch.scrape_menu');"
```

---

## 8. Wix SPA Sites: Homepage Yields 0 Items (JS Shell)

### The Problem

Wix restaurant sites (e.g. `3greeksgrill.com`) serve a JS-framework shell as
the homepage. The static HTML is a ~450KB bundle of Wix bootstrap scripts with
~320 runes of visible boilerplate text and **no menu content**. The menu lives
on a separate Wix SPA route (`/menu`) that is:

1. **Reachable via plain HTTP GET** — Wix serves a prerendered version at
   `https://<site>/menu` with the full menu (prices, items, sections) embedded
   server-side. This is a ~1.1MB page where the menu content (all price
   patterns, item names) starts at ~700KB, after the Wix HTML head and inlined
   script bundles.
2. **NOT discoverable from the homepage's anchors** — the Wix `DropDownMenu`
   widget renders the "MENU" nav button as a `<li>` with a click handler, not
   an `<a href="/menu">` anchor. Neither the static HTML nor the rendered DOM
   (after `networkidle`) contains a discoverable `/menu` link. The only
   `linkElement` hrefs on the homepage are the site root, the Seamless ordering
   link (off-domain, correctly dropped), and `/contact`.
3. **Enqueued as a scrape job by the discovery pipeline** — `discover.go`
   appends `/menu`, `/menu/`, `/menus` as candidate URLs alongside the
   homepage. `menuSignalFilter` must confirm `/menu` before it's enqueued.

The homepage scrape job yielding 0 items is **correct behavior** — there is
no menu on the homepage, even after rendering. The `/menu` job is the one that
should produce items.

### Diagnosis

Look for this log sequence on the homepage job:

```
INFO HTML is a JS-framework shell; menu hydrates client-side url=https://<site> visible_runes=<300
INFO Tier 1: sending to LLM extractor chars=<350
INFO service extractions:structure response sections=0 items=0 ... input_chars=<350
INFO LLM extractor done items=0 ...
INFO text pass empty; JS shell re-cascade via rendered-fetch ...
INFO JS shell re-cascade: re-running LLM on rendered HTML ... rendered_runes=<350
INFO JS shell re-cascade: rendered HTML also yielded 0 items
WARN no menu items extracted url=https://<site>
INFO directory expansion: no candidate sub-URLs found
```

Key indicators:
- `visible_runes` and `rendered_runes` are both ~300–350 (no menu content
  appeared after rendering).
- `sections=0 items=0` on the structure response (the LLM had no menu text to
  extract).
- `directory expansion: no candidate sub-URLs found` (no in-site `/menu` anchor
  exists in the rendered HTML).

If you see this, check whether the `/menu` job ran separately:

```bash
docker compose exec postgres psql -U fodmap -d fodmap -c \
  "SELECT id, args->>'url' AS url, state, attempt
   FROM river.river_job
   WHERE kind = 'menusearch.scrape_menu'
     AND args->>'camis' = '<CAMIS>'
   ORDER BY id;"
```

Look for a job with `url` ending in `/menu`. If it exists and completed
successfully, the menu was extracted from that URL — the homepage job's 0
items is expected. If the `/menu` job failed or doesn't exist, see
[Section 7](#7-river-job-queue-diagnostics) for retry/reset steps.

Also check the restaurant's stored URLs:

```bash
docker compose exec postgres psql -U fodmap -d fodmap -c \
  "SELECT camis, status, website_url, menu_urls FROM restaurants WHERE camis = '<CAMIS>';"
```

If `menu_urls` does not contain a `/menu` entry, the discovery pipeline's
`menuSignalFilter` dropped it — see the `menuSignalFilter` body-read bug below.

### Why rendering doesn't help

The Wix `restaurant-menus-showcase-ooi` widget does not hydrate the menu into
the homepage DOM. The widget's bootstrap config (with `pageUriSEO: ["menu",
"about", ...]`) is in a `<script>` JSON block, but the widget only mounts on
the `/menu` route, not the homepage. Even with `network_idle: true` on the
rendered-fetch, `page.content()` returns the same ~320 runes — the menu is on
a different page, not lazily loaded into the homepage.

### The `IsJSShell` detector and re-cascade

The pipeline has a JS-shell detector (`scraper.IsJSShell`) that flags pages
where the raw HTML is large (≥50KB) but yields <500 runes of visible text at a
raw-to-visible ratio >500×. The detector is **framework-agnostic** — it uses
the raw-bytes-to-visible-runes ratio rather than a list of framework markers,
so it does not rot when Wix renames an asset host or a new SPA framework
appears. Real content pages cluster at 1–200×; JS shells at >1000×. The 500×
threshold sits in a wide empty gap with no observed overlap.

When the text pass returns 0 items on a detected JS shell, the pipeline
attempts a **render-and-re-cascade**: it renders the URL via the webagent's
`FetchRenderedHTML` (with `network_idle: true`), re-converts to Markdown, and
re-runs the LLM extractor on the hydrated HTML.

**Important: the re-cascade runs only AFTER the text pass returns 0 items.**
It does not preempt the text pass. This prevents a regression where a JS shell
that has real static menu content (e.g. a small Wix cafe with ~300 runes of
visible menu text) would have its working text extraction replaced by a
potentially worse hydrated one (dilution). The `jsShell` flag gates only the
post-empty re-cascade, not the preemptive image-OCR or `ScrapeJS` adapter
paths — those still fire only on `IsTooNoisy`/`tooShort`/empty.

This re-cascade is valuable for SPAs where the menu *does* hydrate client-side
(e.g. Toast, Square, custom React apps). For Wix sites specifically, the
re-cascade runs but yields the same empty result because the menu is on a
separate route, not injected into the homepage.

### Fix: ensure the `/menu` job runs

The homepage job is a dead end for Wix sites. The fix is to ensure the
discovery pipeline's `/menu` candidate URL is enqueued and scraped
successfully:

1. Check that `discover.go` appended `/menu` candidates (it always does when
   `result.WebsiteURL` is non-empty — see `discover.go:123`).
2. Check that `menuSignalFilter` confirmed `/menu` (the prerendered Wix page
   passes the price+keyword heuristic — 71 price matches + "menu" keyword).
3. Check the `/menu` scrape job's state in River (see the query above).
4. If the `/menu` job failed, reset it:

```bash
docker compose exec postgres psql -U fodmap -d fodmap -c \
  "UPDATE river.river_job SET state='available', attempt=0, scheduled_at=now()
   WHERE kind='menusearch.scrape_menu'
     AND args->>'camis' = '<CAMIS>'
     AND args->>'url' LIKE '%/menu%';"
```

### `menuSignalFilter` body-read bug (historical)

A previous version of `checkMenuSignal` (`discover.go`) read the response body
with a single `resp.Body.Read(buf)` call, which returns on the first network
chunk (typically ~32KB) — not the full body. For Wix's 1.1MB prerendered
`/menu` page, only 32KB was read and the menu content (at byte ~700KB) was
never seen. `hasMenuSignal` returned false, and `/menu` was silently dropped
as "no menu signal on 2xx GET". The homepage was kept (primary URL, always
kept) and `/menus` was kept (returns 404, always kept), but `/menu` — the
only URL with the actual menu — was never enqueued.

This is now fixed: `checkMenuSignal` uses `io.ReadAll(io.LimitReader(resp.Body,
2*1024*1024))` to drain the full response body up to 2MB. The 2MB cap covers
the largest prerendered pages observed in practice.

If `/menu` is still missing from `menu_urls` after re-running discovery,
verify the running server binary includes this fix — see the stale binary note
below.

### Stale binary note

If the `HTML is a JS-framework shell` log line does not appear on a site that
should trigger it, or if `/menu` is still missing from `menu_urls` after
re-running discovery, the running Go binary may be stale. `start.sh` now
always rebuilds the binary before launching the server (via `go build -o` to a
temp path), but if the server was started with an older `go run . serve`
command, the `IsJSShell` detection, re-cascade code, and `menuSignalFilter`
fix will not be present. Restart with `./start.sh` or `make start`.

To verify the running binary has the fixes, check for the log line:

```
INFO HTML is a JS-framework shell; menu hydrates client-side ...
```

If it does not appear on a known JS-shell site (raw HTML >50KB, visible text
<500 runes), the binary is stale.

---

## 9. Scraper Service Logging (End-to-End Tracing)

### Overview

The scrape pipeline logs at three points so a request can be traced
end-to-end from the Go pipeline through the Python scraper service and back:

1. **Go pipeline** (`pipeline.go`) — logs the tier, input size, and outcome:
   ```
   INFO Tier 1: sending to LLM extractor chars=322
   INFO LLM extractor done url=... items=0 restaurant="3 Greeks Grill"
   ```
   When the JS-shell re-cascade fires:
   ```
   INFO text pass empty; JS shell re-cascade via rendered-fetch ... static_runes=318
   INFO JS shell re-cascade: re-running LLM on rendered HTML ... rendered_runes=321
   INFO JS shell re-cascade: rendered HTML also yielded 0 items
   ```

2. **Go service extractor** (`service_extractor.go`) — logs the
   `extractions:structure` response shape after every call (shared by text,
   PDF, and image paths):
   ```
   INFO service extractions:structure response backend=openai-compat schema_revision=v1 sections=0 items=0 restaurant="3 Greeks Grill" input_chars=335
   ```
   This immediately surfaces the "200 OK but `sections: []`" case — you see
   `sections=0 items=0` instead of just a 200 status code.

3. **Python structuring** (`structuring.py`) — logs the input size, output
   section/item counts, backend, and restaurant name after the LLM call:
   ```
   INFO structuring done: input_chars=322 sections=0 items=0 restaurant=3Greeks Grill backend=openai-compat
   ```

### Diagnosing "200 OK but 0 items"

When the Go log shows `no menu items extracted` but the Python log shows
`200 OK`, use the `service extractions:structure response` log line to
distinguish:

- **`sections=0 items=0`** — the LLM received text but found no menu content.
  This is the correct outcome when the input is homepage boilerplate (no
  menu). Check `input_chars` — if it's ~300–350, the text pass ran on a JS
  shell, not a real menu page. The menu is on a different URL (e.g. `/menu`).
- **`sections=N items=M` (M > 0)** but Go still shows 0 items — a mapping bug
  in `mapStructureToResult`; check the Go-side `structureResult` decoding.
- **No `service extractions:structure response` log line** — the call failed
  before reaching the structuring endpoint; check for a 502/503/504 error.

### Rendered-fetch logging

The `FetchRenderedHTML` path logs:
```
INFO rendered-fetch: calling webagent url=...
INFO rendered-fetch: done url=... html_bytes=453209 duration_ms=2748
```

If `html_bytes` is similar to the static HTML size and `rendered_runes` in the
re-cascade log is similar to `static_runes`, the render did not produce
additional content (the menu is on a different page, not lazily loaded). See
Section 8 for the Wix-specific case.

---

## 10. `network_idle` for JS Widget Hydration

### The Problem

The Python webagent's `/v1/webagent/fetch` endpoint defaults to
`wait_until="domcontentloaded"` and returns `page.content()` immediately. For
JS widgets that fetch menu data via XHR *after* DOM content loaded (e.g.
Toast, Square, custom React apps), the rendered HTML captures the DOM before
the widget hydrates — the menu is absent.

### The Fix

The `/v1/webagent/fetch` endpoint now accepts a `network_idle` field in the
request body:

```json
{"url": "https://example.com", "network_idle": true}
```

When `network_idle: true`, `render_url` waits for `networkidle` (all network
connections settled) after `domcontentloaded` before serializing content. This
gives XHR-driven widgets time to fetch and render their data. The wait is
best-effort: if `networkidle` never fires within the hard-cap timeout, the
render returns whatever content is available.

The Go pipeline's JS-shell re-cascade passes `network_idle: true`
automatically via `RenderOptions{NetworkIdle: true}`. The 403/404 fallback
path (`fetchWithFallback`) does not — blocked pages may never reach
`networkidle`, so the fast `domcontentloaded` return is preferred there.

### When it doesn't help

`network_idle` waits for network traffic to settle, but it does not wait for
a specific widget to mount. Wix's `restaurant-menus-showcase-ooi` widget
loads on the `/menu` route, not the homepage — no amount of waiting on the
homepage will produce the menu. See [Section 8](#8-wix-spa-sites-homepage-yields-0-items-js-shell)
for why Wix homepages are a dead end and the `/menu` route is the fix.
