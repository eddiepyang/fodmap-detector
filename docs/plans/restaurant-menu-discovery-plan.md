# Plan: Restaurant Menu Discovery & Scraping Pipeline

**Status:** Planning — not yet implemented.

## Context

The detector has a working `scrape` command (`cli/scrape.go`) that fetches a
single URL, extracts menu items, embeds them, and upserts into the Weaviate
`RestaurantMenu` collection. It also has a River-based job pipeline in the
`menutracking` package (regulatory tracking) with `ScrapeWorker` /
`RulePromotionWorker` jobs, a `sources` table, and periodic job scheduling.

**Goal:** accept NYC OpenData restaurant inspection records (Socrata API),
store them in a new `restaurants` table, enqueue Gemini-grounded web-search
jobs to find each restaurant's menu URL, then enqueue scrape jobs that run the
existing scrape pipeline — all driven by River. Intermediate data is saved to
a bronze/silver layer in Avro format with event UUIDs for lineage tracking.

### Data source

NYC DOHMH Restaurant Inspection Results (`43nn-pn8j`), ~25k unique restaurants
citywide. **Initial scope: Astoria + Long Island City only** (~1,186 unique
restaurants), fetched via Socrata API with server-side NTA+ZIP filtering.

### Geographic filter (Astoria + LIC)

| NTA | Name | Neighborhood |
|-----|------|-------------|
| QN70 | Astoria | Astoria core, Hallets Cove |
| QN71 | Old Astoria | Old Astoria |
| QN72 | Steinway | Steinway, Ditmars |
| QN68 | Queensbridge-Ravenswood-LIC | Dutch Kills, Queensbridge, Ravenswood |
| QN31 (restricted to zips 11101, 11109) | Hunters Point | Hunters Point (LIC), excluding Sunnyside (11104) |

SoQL filter:
```
nta IN ('QN70','QN71','QN72','QN68')
OR (nta='QN31' AND zipcode IN ('11101','11109'))
```

### Key decisions (confirmed with user)

- **Discovery agent:** Gemini + `GoogleSearch` tool (built-in web search
  grounding). Reuses existing `GOOGLE_API_KEY` / `genai.Client`.
- **Two-phase:** `discover` job (find menu URL) → `scrape` job (extract +
  embed + upsert). Separate River job kinds.
- **Package:** new `menusearch` package (clean separation from
  `menutracking`'s regulatory domain).
- **Interface:** CLI command + REST API.
- **Seed data:** Postgres `restaurants` table (durable state, queryable).
- **Bronze/silver layer:** Avro OCF files for intermediate data, with event
  UUIDs, River job IDs, attempt numbers, and cross-stage lineage.
- **Pipeline extraction:** extract `runScrapeWith` into a new `pipeline`
  package (not `scraper/pipeline.go`) to avoid `scraper` → `server` layering
  violations.
- **Single River client:** register `menusearch` workers in the same
  `river.Workers` bundle as `menutracking` — one client, one pool, one
  leader election.

---

## Architecture

```
Socrata API (NTA+ZIP filter)
    │
    v
fodmap restaurants import --area astoria-lic
    │
    ├─ fetch CSV from Socrata API
    ├─ ParseNYCCSV() → dedup by camis (keep latest inspection_date)
    ├─ write nyc_restaurant.avro to bronze (with event_id + created_at)
    ├─ upsert into restaurants table (status = 'pending_discovery')
    └─ enqueue DiscoverMenuURL jobs (UniqueOpts: ByArgs + ByPeriod)
                                                    │
                          River job: menusearch.discover_menu_url
                                                    │
                                                    v
                                    ┌───────────────────────────────┐
                                    │  DiscoverMenuURLWorker         │
                                    │  - Gemini GoogleSearch prompt   │
                                    │  - Parse URLs from response     │
                                    │    text + GroundingChunks       │
                                    │  - Write gemini_discovery.avro  │
                                    │    (event_id, job_id, attempt)  │
                                    │  - Update restaurants row:      │
                                    │    menu_url, status='url_found' │
                                    │  - Enqueue ScrapeMenuJob with   │
                                    │    DiscoveryEventID             │
                                    └──────────┬─────────────────────┘
                                               │
                                               v
                                     ┌───────────────────────────────┐
                                     │  ScrapeMenuWorker              │
                                     │  - ExtractMenu (fetch+extract) │
                                     │  - Write {camis}-{attempt}.html│
                                     │  - Write menu_extraction.avro  │
                                     │    (event_id, job_id, attempt,  │
                                     │     discovery_event_id)        │
                                     │  - StoreMenu (embed→Weaviate)  │
                                     │  - Update row:                 │
                                     │    status='scraped',           │
                                     │    item_count, scraped_at      │
                                     └───────────────────────────────┘
```

---

## Avro Bronze/Silver Layer

### Directory layout

```
data/bronze/
  nyc_opendata/
    {YYYY-MM-DD}/
      astoria-lic.avro          ← nyc_restaurant records (batch, deduped)
  gemini/
    {YYYY-MM-DD}/
      {camis}-{attempt}.avro    ← gemini_discovery record (per job attempt)
  restaurants/
    {YYYY-MM-DD}/
      {camis}-{attempt}.html    ← raw scraped HTML (bronze, like menutracking)
      {camis}-{attempt}.avro    ← menu_extraction record (silver, post-LLM)
```

Filenames include `{attempt}` (the River job attempt number, 1-based) so
retry runs don't overwrite earlier attempts — each attempt's intermediate
data is preserved for debugging and lineage.

### Avro schemas

All schemas live in `data/schemas/schemas.go` alongside the existing
`EventSchema` (`yelp_reviews`). All use `string` for timestamps
(ISO 8601, `time.Now().UTC().Format(time.RFC3339)`) — consistent with the
existing `scraped_at` pattern.

#### 1. `nyc_restaurant` (bronze — Socrata CSV ingestion)

```json
{
    "type": "record",
    "name": "nyc_restaurant",
    "fields": [
        {"name": "camis", "type": "string"},
        {"name": "dba", "type": "string"},
        {"name": "boro", "type": "string"},
        {"name": "building", "type": "string"},
        {"name": "street", "type": "string"},
        {"name": "zipcode", "type": "string"},
        {"name": "phone", "type": "string"},
        {"name": "cuisine_description", "type": "string"},
        {"name": "inspection_date", "type": "string"},
        {"name": "latitude", "type": "double"},
        {"name": "longitude", "type": "double"},
        {"name": "nta", "type": "string"},
        {"name": "record_date", "type": "string"},
        {"name": "event_id", "type": "string"},
        {"name": "created_at", "type": "string"}
    ]
}
```

- Written by: `fodmap restaurants import` command
- One file per import run: `data/bronze/nyc_opendata/{YYYY-MM-DD}/astoria-lic.avro`
- `event_id` = `uuid.NewString()` per record
- `created_at` = ingestion timestamp
- `record_date` = the city's extract date (from CSV column)
- Empty CSV fields → `""` (plain `string`, no nullable unions — consistent
  with existing `yelp_reviews` schema)

#### 2. `gemini_discovery` (bronze — Gemini response)

```json
{
    "type": "record",
    "name": "gemini_discovery",
    "fields": [
        {"name": "camis", "type": "string"},
        {"name": "dba", "type": "string"},
        {"name": "prompt", "type": "string"},
        {"name": "response_text", "type": "string"},
        {"name": "source_urls", "type": {"type": "array", "items": "string"}},
        {"name": "model", "type": "string"},
        {"name": "event_id", "type": "string"},
        {"name": "job_id", "type": "string"},
        {"name": "attempt", "type": "int"},
        {"name": "created_at", "type": "string"}
    ]
}
```

- Written by: `DiscoverMenuURLWorker` after Gemini call
- One file per job attempt: `data/bronze/gemini/{YYYY-MM-DD}/{camis}-{attempt}.avro`
- `event_id` = `uuid.NewString()` — passed to `ScrapeMenuJobArgs.DiscoveryEventID`
- `job_id` = `job.ID` from River
- `attempt` = `job.Attempt` from River (1-based)
- `created_at` = timestamp when Gemini response received

#### 3. `menu_extraction` (silver — post-LLM, pre-embedding)

```json
{
    "type": "record",
    "name": "menu_extraction",
    "fields": [
        {"name": "camis", "type": "string"},
        {"name": "source_url", "type": "string"},
        {"name": "restaurant_name", "type": "string"},
        {"name": "items", "type": {
            "type": "array",
            "items": {
                "type": "record",
                "name": "menu_item",
                "fields": [
                    {"name": "dish_name", "type": "string"},
                    {"name": "description", "type": "string"},
                    {"name": "stated_ingredients", "type": {"type": "array", "items": "string"}},
                    {"name": "has_full_ingredients", "type": "boolean"}
                ]
            }
        }},
        {"name": "event_id", "type": "string"},
        {"name": "job_id", "type": "string"},
        {"name": "attempt", "type": "int"},
        {"name": "discovery_event_id", "type": "string"},
        {"name": "created_at", "type": "string"}
    ]
}
```

- Written by: `ScrapeMenuWorker` after LLM extraction, before embedding
- One file per job attempt: `data/bronze/restaurants/{YYYY-MM-DD}/{camis}-{attempt}.avro`
- `event_id` = `uuid.NewString()`
- `job_id` = `job.ID` from River
- `attempt` = `job.Attempt` from River
- `discovery_event_id` = passed from `ScrapeMenuJobArgs.DiscoveryEventID`
  (traces back to the Gemini discovery record)
- `created_at` = timestamp when `MenuExtractionResult` produced

### Lineage chain

```
nyc_restaurant.event_id
    → gemini_discovery.event_id (found the menu URL)
        → menu_extraction.discovery_event_id (scraped the menu)
            → Weaviate RestaurantMenu (embedded + searchable)
```

### Implementation

- Reuse the existing `data/io/event.go` `EventWriter` — it already handles
  `map[string]any` records via `hamba/avro/v2/ocf`.
- All three schemas go in `data/schemas/schemas.go`.
- **`nyc_restaurant` records must bypass `EventWriter.Write`** — the
  existing `Write` method coerces all `float64` values to `float32`
  (designed for the `yelp_reviews` schema which uses Avro `float`). The
  `nyc_restaurant` schema uses `double` for `latitude`/`longitude`;
  the coercion loses precision (`40.75664132086` → `40.75664138793945`).
  Use `ocf.NewEncoder` + `encoder.Encode(record)` directly for
  `nyc_restaurant` records, or add a `WriteRaw` method to `EventWriter`
  that skips the coercion step.
- `gemini_discovery` and `menu_extraction` have no `double` fields —
  `EventWriter.Write` is safe for them.
- Write failures emit `slog.Warn` and do not abort the job (same pattern as
  `menutracking/workers.go:110-112`) — the bronze layer is best-effort
  audit storage, not a transactional dependency.

---

## New Files

| File | Purpose |
|---|---|
| `internal/db/migrations/000002_restaurants.up.sql` | `restaurants` table + indexes |
| `internal/db/migrations/000002_restaurants.down.sql` | Drop table |
| `pipeline/pipeline.go` | Extracted `ExtractMenu` + `StoreMenu` + `ToMenuItems` + `ExtractPDF` from `cli/scrape.go` |
| `menusearch/restaurant.go` | `Restaurant` struct + status constants |
| `menusearch/store.go` | `//go:embed` SQL strings; implements `server.RestaurantStore` |
| `menusearch/store/sql/upsert_restaurant.sql` | Upsert by camis |
| `menusearch/store/sql/get_restaurant.sql` | Get by camis |
| `menusearch/store/sql/list_restaurants.sql` | List with optional status/search filters |
| `menusearch/store/sql/update_menu_url.sql` | Set menu_url + status |
| `menusearch/store/sql/update_scrape_result.sql` | Set status + scraped_at + item_count + last_error |
| `menusearch/areas.go` | Area name → NTA+ZIP filter mapping |
| `menusearch/nycdata.go` | Socrata API HTTP client + SoQL query builder |
| `menusearch/csv.go` | NYC OpenData CSV parser + dedup |
| `menusearch/csv_test.go` | CSV parsing/dedup tests |
| `menusearch/discover.go` | `DiscoverMenuURLWorker` + `GeminiSearcher` interface + `GeminiMenuSearcher` impl |
| `menusearch/discover_test.go` | Tests for discover worker (stub searcher) |
| `menusearch/scrape.go` | `ScrapeMenuWorker` — calls `pipeline.RunScrape` |
| `menusearch/scrape_test.go` | Tests for scrape worker (stub extractor) |
| `menusearch/workers.go` | Job args types + `Kind()` methods + `RiverInserter` interface |
| `menusearch/workers_test.go` | River worker tests with stubs |
| `menusearch/avro.go` | Avro record writers for the three schemas |
| `cli/restaurants.go` | `fodmap restaurants import/list/scrape/discover/retry` commands |
| `cli/restaurants_test.go` | CLI tests |
| `server/restaurants_handler.go` | REST handlers: CRUD + trigger endpoints |
| `server/restaurant_store.go` | `RestaurantStore` + `RiverInserter` interfaces + `Restaurant` struct (shared with menusearch) |
| `server/restaurants_handler_test.go` | Handler tests |

---

## Database Schema

### Migration `000002_restaurants.up.sql`

```sql
CREATE TABLE IF NOT EXISTS restaurants (
    camis           TEXT PRIMARY KEY,          -- NYC DOHMH unique ID
    dba             TEXT NOT NULL,              -- doing-business-as name
    boro            TEXT,
    building        TEXT,
    street          TEXT,
    zipcode         TEXT,
    phone           TEXT,
    cuisine         TEXT,
    latitude        DOUBLE PRECISION,
    longitude       DOUBLE PRECISION,
    nta             TEXT,
    -- Discovery + scrape lifecycle
    status          TEXT NOT NULL DEFAULT 'pending_discovery',
    menu_url        TEXT,
    menu_url_source TEXT,                       -- 'gemini' | 'manual'
    item_count      INTEGER DEFAULT 0,
    scraped_at      TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_restaurants_status ON restaurants(status);
CREATE INDEX IF NOT EXISTS idx_restaurants_dba ON restaurants USING gin (to_tsvector('english', dba));
CREATE INDEX IF NOT EXISTS idx_restaurants_nta ON restaurants(nta);
```

### Status state machine

```
pending_discovery ──(discover job)──► url_found ──(scrape job)──► scraped
                     │                                              │
                     ├──(no URL found)──► no_url_found              │
                     └──(error)──► failed_discovery                │
                                                                    │
                     scraping ──(error)──► failed_scrape ◄──────────┘

retry command resets: failed_* → pending_discovery (or url_found if menu_url set)
```

---

## River Job Kinds

### 1. `menusearch.discover_menu_url`

```go
type DiscoverMenuURLJobArgs struct {
    CAMIS    string `json:"camis" jsonschema:"required"`
    DBA      string `json:"dba" jsonschema:"required"`
    Building string `json:"building" jsonschema:"required"`
    Street   string `json:"street" jsonschema:"required"`
    Boro     string `json:"boro" jsonschema:"required"`
}
func (DiscoverMenuURLJobArgs) Kind() string { return "menusearch.discover_menu_url" }
```

**Worker:** `DiscoverMenuURLWorker`
- Calls Gemini with `GoogleSearch` tool: "Find the menu page URL for {DBA}
  at {Building} {Street}, {Boro} NY. Return only the URL(s), one per line."
- Extracts URLs from both `resp.Text()` (via URL regex:
  `https?://[^\s)+"']`) and
  `Candidate.GroundingMetadata.GroundingChunks[*].Web.URI`.
- Deduplicates + filters URLs (drop `facebook.com`, `instagram.com`,
  `yelp.com`, `tripadvisor.com`, `google.com/maps`, `ubereats.com`,
  `doordash.com`, `grubhub.com`, `postmates.com`, `seamless.com`). Keep
  the restaurant's own domain + any `*/menu` path.
- Generates `event_id` (`uuid.NewString()`).
- Writes `gemini_discovery.avro` record to bronze (best-effort — `slog.Warn`
  on failure, does not abort).
- If URL found: `UPDATE restaurants SET menu_url=$1, status='url_found'`,
  enqueue `ScrapeMenuJobArgs` with `DiscoveryEventID` set.
- If no URL: `status='no_url_found'`.
- On error: River retries (status stays `pending_discovery`).

**Unique opts:** `UniqueOpts{ByArgs: true, ByPeriod: 30 * 24 * time.Hour}`
— dedupes by CAMIS within a 30-day window. Re-running `import` won't
create duplicate discover jobs.

### 2. `menusearch.scrape_menu`

```go
type ScrapeMenuJobArgs struct {
    CAMIS              string `json:"camis" jsonschema:"required"`
    MenuURL             string `json:"menu_url" jsonschema:"required"`
    DiscoveryEventID    string `json:"discovery_event_id" jsonschema:"required"`
}
func (ScrapeMenuJobArgs) Kind() string { return "menusearch.scrape_menu" }
```

**Worker:** `ScrapeMenuWorker`
- Reads `menu_url` from job args.
- Sets `status='scraping'`.
- Calls `pipeline.ExtractMenu(ctx, menuURL, fetcher, extractor, ...)` —
  fetch + extract cascade. Returns `*scraper.MenuExtractionResult`.
- Writes raw HTML to `data/bronze/restaurants/{date}/{camis}-{attempt}.html`
  (best-effort, like `menutracking`).
- Generates `event_id` (`uuid.NewString()`).
- Writes `menu_extraction.avro` record to
  `data/bronze/restaurants/{date}/{camis}-{attempt}.avro` (best-effort).
- Calls `pipeline.StoreMenu(ctx, result, menuURL, menuStore, embedder)` —
  embed + upsert to Weaviate. Returns `itemCount`.
- On success: `status='scraped'`, `item_count=itemCount`, `scraped_at=NOW()`.
- On error: `status='failed_scrape'`, `last_error=err.Error()`.

**Unique opts:** `UniqueOpts{ByArgs: true, ByPeriod: 30 * 24 * time.Hour}`.

**Worker struct fields (scrape flags passed from `serve.go`):**

```go
type ScrapeMenuWorker struct {
    river.WorkerDefaults[ScrapeMenuJobArgs]

    Pool             *pgxpool.Pool
    Fetcher          scraper.Fetcher
    Extractor        scraper.Extractor
    MenuStore        server.MenuStore
    Embedder         search.Embedder
    EnableVision     bool
    EnableJSRender    bool
    UsePdftotext      bool
    WebagentAdapter   string
    BronzeDir         string  // override for tests
}
```

---

## Pipeline Extraction (`pipeline` package)

### Problem

`runScrapeWith` lives in `cli/scrape.go` (package `cli`). The
`ScrapeMenuWorker` (in `menusearch`) needs to call it, but can't import
`cli` (circular dep). Moving it to `scraper/pipeline.go` would make `scraper`
(currently a leaf package with zero internal `fodmap/*` imports) depend on
`server` (for `MenuStore`) and `search` (for `Embedder`/`MenuItem`) — an
architectural layering violation.

### Solution

Create a new `pipeline` package that imports `scraper`, `search`, and
`server` — a composition layer. Both `cli/scrape.go` and
`menusearch/scrape.go` import it.

### Split: `ExtractMenu` + `StoreMenu`

`runScrapeWith` currently returns only `error` — the `MenuExtractionResult`
and item count are lost (printed via `fmt.Printf` then discarded). The
`ScrapeMenuWorker` needs both: the result for the Avro `menu_extraction`
record, and the count for the `restaurants` row update.

Split `runScrapeWith` into two phases so the worker can write the Avro
record **between** extraction and storage:

```go
// ExtractMenu fetches the URL, runs the extraction cascade (JSON-LD →
// HTML/PDF → image → JS-render), and returns the structured result.
// This is the "acquire + extract" phase — no embedding, no storage.
func ExtractMenu(
    ctx context.Context,
    rawURL string,
    fetcher scraper.Fetcher,
    ex scraper.Extractor,
    enableVision bool,
    enableJSRender bool,
    usePdftotext bool,
    webagentAdapter string,
) (*scraper.MenuExtractionResult, error)

// StoreMenu embeds the extracted items and upserts them into the menu
// store (Weaviate). Returns the item count. This is the "embed + persist"
// phase.
func StoreMenu(
    ctx context.Context,
    result *scraper.MenuExtractionResult,
    rawURL string,
    store server.MenuStore,
    embedder search.Embedder,
) (int, error)
```

The CLI's `runScrape` calls both in sequence (replacing `runScrapeWith`).
The `ScrapeMenuWorker` calls `ExtractMenu`, writes the Avro record, then
calls `StoreMenu`.

### What moves

Three functions move from `cli/scrape.go` to `pipeline/pipeline.go`:

| Function | Current location | New signature |
|---|---|---|
| `runScrapeWith` | `cli/scrape.go:160` | Split into `ExtractMenu` + `StoreMenu` (above) |
| `toMenuItems` | `cli/scrape.go:430` | `func ToMenuItems(ctx, result, rawURL, embedder) ([]search.MenuItem, error)` — called by `StoreMenu` |
| `extractPDF` | `cli/scrape.go:370` | `func ExtractPDF(ctx, pdfBytes, usePdftotext, enableVision, ex) (string, *scraper.MenuExtractionResult, error)` — called by `ExtractMenu` |

The `menuCollectionNS` UUID namespace variable (`cli/scrape.go:426`) moves
with `ToMenuItems`.

### Changes during extraction

- Replace the two `fmt.Printf` calls in `runScrapeWith` (lines 338, 354)
  with `slog.Info` — the function is now library code, not CLI code.
  The CLI wrapper (`runScrape`) prints its own summary from the returned
  `*MenuExtractionResult` + item count.
- `cli/scrape.go`'s `runScrape` (the cobra wrapper) calls
  `pipeline.ExtractMenu` then `pipeline.StoreMenu`.
- `menusearch/scrape.go`'s `ScrapeMenuWorker.Work` calls `ExtractMenu`,
  writes Avro + HTML bronze, then calls `StoreMenu`.

### Import safety

Verified: `scraper` does not import `server` or `search`. `server` does not
import `scraper`. `search` does not import `scraper`. The `pipeline` package
sits above all three — no circular deps.

---

## Gemini Grounding Call (`menusearch/discover.go`)

```go
type GeminiSearcher interface {
    Search(ctx context.Context, dba, building, street, boro string) ([]string, error)
}

type GeminiMenuSearcher struct {
    Client *genai.Client
    Model  string  // "gemini-2.5-flash"
}

func (g *GeminiMenuSearcher) Search(ctx context.Context, dba, building, street, boro string) ([]string, error) {
    prompt := fmt.Sprintf(
        "Find the menu page URL for %s at %s %s, %s, NY. "+
            "Return only the URL(s), one per line.",
        dba, building, street, boro)

    cfg := &genai.GenerateContentConfig{
        Tools: []*genai.Tool{
            {GoogleSearch: &genai.GoogleSearch{}},
        },
    }

    resp, err := g.Client.Models.GenerateContent(ctx, g.Model, genai.Text(prompt), cfg)
    if err != nil {
        return nil, fmt.Errorf("gemini generate: %w", err)
    }

    var urls []string

    // Primary: extract URLs from grounding chunks (authoritative sources).
    for _, cand := range resp.Candidates {
        if cand.GroundingMetadata == nil {
            continue
        }
        for _, chunk := range cand.GroundingMetadata.GroundingChunks {
            if chunk.Web != nil && chunk.Web.URI != "" {
                urls = append(urls, chunk.Web.URI)
            }
        }
    }

    // Fallback: extract URLs from response prose via regex.
    urlRe := regexp.MustCompile(`https?://[^\s)+"']+`)
    for _, u := range urlRe.FindAllString(resp.Text(), -1) {
        urls = append(urls, u)
    }

    return dedupAndFilter(urls), nil
}
```

**Model:** `gemini-2.5-flash` (stable, supports `GoogleSearch` tool).
Configurable via `--discover-model` flag / `DISCOVER_MODEL` env.

**URL extraction strategy:** `GroundingChunks` are authoritative (the
sources Gemini actually cited). The regex fallback catches URLs in prose
that Gemini mentions but doesn't formally cite. Union both, dedup, filter.

**Stubbable:** Tests inject a `stubSearcher` implementing `GeminiSearcher`
with canned URLs.

---

## Socrata API Client (`menusearch/nycdata.go`)

```go
// FetchNYCRestaurants downloads restaurant records from the NYC OpenData
// Socrata API, filtered by the given area's NTA+ZIP codes.
func FetchNYCRestaurants(ctx context.Context, area string, appToken string) (io.ReadCloser, error)
```

- Base URL: `https://data.cityofnewyork.us/resource/43nn-pn8j.csv`
- SoQL `$where` filter built from the area mapping (`menusearch/areas.go`).
- `$select` projects only the columns we need (camis, dba, boro, building,
  street, zipcode, phone, cuisine_description, inspection_date, latitude,
  longitude, nta, record_date).
- `X-App-Token` header set if `--nyc-app-token` / `NYC_APP_TOKEN` provided
  (raises Socrata throttle limits).
- Returns the HTTP response body (`io.ReadCloser`) for the CSV parser to
  stream — doesn't buffer the full response in memory.

### Area mapping (`menusearch/areas.go`)

```go
var Areas = map[string]AreaFilter{
    "astoria-lic": {
        NTAs:   []string{"QN70", "QN71", "QN72", "QN68"},
        NTAZipRestrict: map[string][]string{
            "QN31": {"11101", "11109"},
        },
    },
}
```

---

## CSV Parsing (`menusearch/csv.go`)

```go
type NYCRestaurantRecord struct {
    CAMIS              string
    DBA                string
    Boro               string
    Building           string
    Street             string
    Zipcode            string
    Phone              string
    CuisineDescription string
    InspectionDate     string
    Latitude           float64
    Longitude          float64
    NTA                string
    RecordDate         string
}

// ParseNYCCSV reads the NYC OpenData CSV and returns deduplicated records
// keyed by CAMIS. When multiple rows share a CAMIS, the one with the most
// recent inspection_date wins.
func ParseNYCCSV(r io.Reader) ([]NYCRestaurantRecord, error)
```

Dedup: `map[camis]NYCRestaurantRecord`, replace if
`parseDate(row.InspectionDate).After(parseDate(existing.InspectionDate))`.
Rows with `inspection_date = 01/01/1900` are kept (newly permitted, no
inspection yet) but lose to any row with a real date.

Filters: drop `boro = "0"` (missing borough), `latitude = 0` (failed
geocoding).

---

## CLI Commands

### `fodmap restaurants import --area astoria-lic`

```sh
fodmap restaurants import --area astoria-lic \
    --postgres-dsn "$POSTGRES_DSN" \
    --nyc-app-token "$NYC_APP_TOKEN"   # optional, raises Socrata throttle limits
    --limit 100                         # cap rows (default: all)
    --skip-discovery                    # just upsert rows, don't enqueue jobs
```

Flow:
1. Resolve area → NTA+ZIP filter (`menusearch.Areas`).
2. Fetch CSV from Socrata API (`FetchNYCRestaurants`).
3. Parse + dedup by camis (`ParseNYCCSV`).
4. Write `nyc_restaurant.avro` to bronze (with `event_id` + `created_at`).
5. Upsert each into `restaurants` table.
6. For rows with `status = 'pending_discovery'`, insert
   `DiscoverMenuURLJobArgs` into River (with `UniqueOpts{ByArgs: true,
   ByPeriod: 30 * 24 * time.Hour}`).

### `fodmap restaurants list`

```sh
fodmap restaurants list \
    --postgres-dsn "$POSTGRES_DSN" \
    --status pending_discovery \
    --limit 50
```

### `fodmap restaurants scrape <camis>`

```sh
fodmap restaurants scrape 50165827 \
    --postgres-dsn "$POSTGRES_DSN" \
    --extractor-url http://localhost:8765
```

Enqueues a `ScrapeMenuJobArgs` (requires `--menu-url` or a `menu_url`
already set on the row).

### `fodmap restaurants discover <camis>`

```sh
fodmap restaurants discover 50165827 \
    --postgres-dsn "$POSTGRES_DSN"
```

### `fodmap restaurants retry <camis>`

Resets a failed restaurant to retry: sets `status='pending_discovery'` (or
`url_found` if `menu_url` is set) and re-enqueues the appropriate job.

```sh
fodmap restaurants retry 50165827 \
    --postgres-dsn "$POSTGRES_DSN"
```

---

## REST API

### `POST /api/v1/restaurants` (admin)

Add a single restaurant + enqueue discovery:

```json
{
    "camis": "50165827",
    "dba": "AB Stable LLC",
    "boro": "Manhattan",
    "building": "301",
    "street": "PARK AVENUE",
    "zipcode": "10022",
    "phone": "5182826019",
    "cuisine": "",
    "latitude": 40.756641,
    "longitude": -73.974358
}
```

### `GET /api/v1/restaurants` (admin)

Query: `?status=scraped&limit=50&offset=0&search=pizza`

### `GET /api/v1/restaurants/{camis}` (admin)

Single restaurant detail.

### `POST /api/v1/restaurants/{camis}/scrape` (admin)

Manually trigger a scrape job (re-scrape).

### `POST /api/v1/restaurants/{camis}/discover` (admin)

Manually trigger a discovery job.

### `POST /api/v1/restaurants/{camis}/retry` (admin)

Reset a failed restaurant and re-enqueue the appropriate job.

---

## Worker Registration

### Single River client

`menusearch` workers are registered in the **same** `river.Workers` bundle
as `menutracking` workers — one client, one pool, one leader election. This
avoids double leader-election/maintenance overhead and keeps periodic-job
ownership in one place.

### Pool sharing

The `menutracking` pipeline creates a `*pgxpool.Pool` in
`StartMenutrackingPipeline` and exposes it via `PipelineResult.Pool`. The
`menusearch` workers + restaurant store share this pool (both touch the same
Postgres + River tables). If `menutracking` is not enabled, `menusearch`
creates its own pool.

### Server interfaces (avoid `server` → `menusearch` import)

Following the `MenutrackingAdmin` pattern, the `server` package defines
interfaces that `menusearch` implements. `serve.go` wires the concrete
implementations.

```go
// server/restaurant_store.go

// RestaurantStore manages the restaurants table. Implemented by
// menusearch.store; defined here so server handlers don't import menusearch.
type RestaurantStore interface {
    Upsert(ctx context.Context, r Restaurant) error
    Get(ctx context.Context, camis string) (*Restaurant, error)
    List(ctx context.Context, status string, search string, limit, offset int) ([]Restaurant, error)
    UpdateMenuURL(ctx context.Context, camis, menuURL, source string) error
    UpdateScrapeResult(ctx context.Context, camis, status string, itemCount int, lastError string) error
}

// RiverInserter inserts River jobs. Same interface as menutracking's.
type RiverInserter interface {
    Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}
```

The `Restaurant` struct is defined in `server` (not `menusearch`) so both
packages share it without a circular import. The `menusearch` package's
store implements `server.RestaurantStore` via structural typing.

### New flags on `serveCmd`

The `ScrapeMenuWorker` needs the same extractor flags as `fodmap scrape`.
Add these to `serveCmd` (and bind to viper):

- `--extractor-url` — Python scraper service base URL
- `--llm-url` — OpenAI-compatible LLM endpoint
- `--llm-model` — LLM model name
- `--llm-api-key` — LLM API key
- `--enable-vision` — pure-Go vision fallback
- `--pdftotext` — system pdftotext fallback
- `--enable-js-render` — route JS pages to webagent
- `--webagent-adapter` — webagent adapter ID
- `--discover-model` — Gemini model for discovery (default: `gemini-2.5-flash`)
- `--nyc-app-token` — Socrata API app token (optional)
- `--enable-restaurant-pipeline` — gate the menusearch workers (like
  `--enable-pipeline` gates menutracking)

**Important: `serve` flag defaults differ from `scrape`.** The `scrape`
command defaults `--llm-url` to `http://localhost:8000/v1`. On `serve`,
`--llm-url` defaults to `""` so we can detect "not configured." The
extractor is only built when `--enable-restaurant-pipeline` is true. If
`--llm-url` is empty, the worker uses a basic `OpenAICompatExtractor` with
its own default; if `--extractor-url` is set, wrap in `ServiceExtractor`.

### Startup function

```go
// StartMenuSearchPipeline starts the menusearch River workers and returns
// a result with a stop function + pool. Called from serve.go when
// --enable-restaurant-pipeline is set.
func StartMenuSearchPipeline(ctx context.Context, cfg MenuSearchPipelineConfig) (*MenuSearchPipelineResult, error)
```

`MenuSearchPipelineConfig` receives the fetcher, extractor, menuStore,
embedder, genaiClient, and pool (shared from menutracking or newly created).
`MenuSearchPipelineResult` exposes `Pool` and `Stop` — the pool is wired to
a `RestaurantStore` for the REST handlers via `srv.SetRestaurantStore(...)`.

### Worker startup ordering

River calls `Work` on a goroutine — there's a race if a job is already
queued before the client is fully wired. The startup function must follow
this ordering:

1. Construct worker structs (with `RiverClient: nil` on
   `DiscoverMenuURLWorker`).
2. Register workers in `river.NewWorkers()`.
3. Create `river.NewClient(riverpgxv5.New(pool), ...)` — this does not
   start processing yet.
4. Set `discoverWorker.RiverClient = riverClient` — wire the client
   field so the discover worker can enqueue scrape jobs.
5. Call `riverClient.Start(ctx)` — now workers begin processing.

This mirrors the existing `menutracking` pattern
(`menutracking_migrate.go:197-210`: create client → set
`scrapeWorker.RiverClient` → `Start`).

---

## Dependencies

- **No new Go deps.** `google.golang.org/genai` is already in `go.mod`
  (v1.51.0). `encoding/csv` and `regexp` are stdlib. River, `hamba/avro/v2`,
  and `github.com/google/uuid` are already deps.
- **No new Python deps.** The scraper service is unchanged.

---

## Testing

Per `.rules/testing.md`: TDD, stubs not mocks, `make check` = lint + test + build.

### Unit tests

- `menusearch/csv_test.go` — CSV parsing, dedup, date sentinel handling,
  missing-borough filter, lat=0 filter.
- `menusearch/discover_test.go` — `DiscoverMenuURLWorker.Work` with a
  `stubSearcher` returning canned URLs; verify row updated to `url_found`,
  scrape job enqueued with `DiscoveryEventID`. No-URL case →
  `no_url_found`. Error case → status unchanged (River retries). Verify
  Avro record written.
- `menusearch/scrape_test.go` — `ScrapeMenuWorker.Work` with a
  `stubExtractor` + `stubMenuStore` + `stubEmbedder`; verify
  `BatchUpsertMenu` called, row updated to `scraped` with `item_count`.
  Error case → `failed_scrape`. Verify Avro record + HTML bronze written.
- `menusearch/workers_test.go` — River `Kind()` constants, job args
  serialization roundtrip.
- `menusearch/avro_test.go` — Avro record roundtrip for all three schemas
  (write + read back, verify `event_id`, `job_id`, `attempt`,
  `discovery_event_id` fields).
- `cli/restaurants_test.go` — `import` command with a small CSV fixture,
  stubbed River inserter, verify rows upserted + jobs enqueued + Avro
  file written.
- `server/restaurants_handler_test.go` — REST handlers with stubbed store +
  River inserter.

### Integration

- `make check` runs all unit tests + lint + build.
- Manual e2e: run `fodmap restaurants import --area astoria-lic --limit 10
  --postgres-dsn ...`, verify rows in DB + jobs in River + Avro in bronze.
  Start workers, verify discovery + scrape complete + Avro records written.

---

## Risks and Gaps

- **Gemini web search rate limits.** The free tier has a daily cap on
  grounded requests (unverified — docs site was unreachable during
  research). For ~1,186 restaurants (Astoria+LIC scope), discovery at 5
  req/s takes ~4 minutes. Even at 1 req/s it's under 20 minutes.
  Mitigation: `--limit` flag on import, `--skip-discovery` to upsert rows
  without enqueuing, River's per-job retry/backoff handles 429s.

- **URL quality.** Gemini may return a Yelp/DoorDash URL instead of the
  restaurant's own site. The URL filter drops known delivery/review
  domains, but if the restaurant has no website, discovery yields
  `no_url_found`. This is expected — not every NYC restaurant has an
  online menu.

- **Scrape jobs are long-running.** `pipeline.ExtractMenu` + `StoreMenu`
  can take minutes (especially PDF/OCR via the scraper service). River's
  `MaxWorkers: 5` bounds concurrency; increase if throughput matters. The
  `--extractor-url` flag is passed through to the worker so it uses the
  Python service for hard cases.

- **`EventWriter.Write` float64→float32 coercion.** The existing
  `EventWriter.Write` method (`data/io/event.go:30-38`) blindly coerces
  all `float64` values to `float32`. This is correct for the
  `yelp_reviews` schema (Avro `float`) but corrupts `double` fields
  (`latitude`/`longitude` in `nyc_restaurant`). Verified: `40.75664132086`
  → `40.75664138793945`. Fix: bypass `EventWriter.Write` for
  `nyc_restaurant` records — use `ocf.NewEncoder` + `Encode` directly.

- **`RunScrape` signature change.** The original `runScrapeWith` returns
  only `error` — the `MenuExtractionResult` and item count are lost.
  Split into `ExtractMenu` (returns `*MenuExtractionResult`) +
  `StoreMenu` (returns `int` item count) so the worker can write the
  Avro record between phases and update the DB row with the count.

- **No Postgres backend for menus.** `MenuStore` is Weaviate-only
  (`search/weaviate.go:1066`). The scrape worker stores items in Weaviate,
  not Postgres. If Postgres menu storage is needed later, add
  `BatchUpsertMenu` to `PostgresClient` + a `menu_items` table — out of
  scope here.

- **Discovery prompt quality.** The prompt "Find the menu page URL for
  {DBA} at {address}, {boro} NY" may need tuning. Some restaurants have
  generic names (e.g. "UN (COFFEE STUDIO) D") that Gemini may not find.
  The grounding metadata provides source URLs for transparency, and
  `menu_url_source='gemini'` tracks provenance for manual review.

- **Re-discovery / re-scraping.** The `restaurants scrape <camis>` CLI,
  `POST /api/v1/restaurants/{camis}/scrape`, and `retry` command/endpoint
  allow manual re-triggering. A periodic re-scrape (like menutracking's
  `@weekly` cron) is a natural follow-on but not in scope.

- **Restaurant ↔ MenuExtractionResult linkage.** The scrape worker sets
  `BusinessID = scraper.BusinessID(menuURL)` (derived from URL hostname),
  not from CAMIS. To link a Weaviate `RestaurantMenu` object back to a
  Postgres `restaurants` row, the `menu_url` is the join key. The Avro
  `menu_extraction` record carries `camis` directly, providing the
  lineage bridge. Consider adding `camis` as a Weaviate property on
  `MenuItem` for direct joins — a follow-on schema migration, not in
  scope here.

- **Single River queue.** Both job kinds use `river.QueueDefault`. If
  discovery (fast, Gemini-bound) and scraping (slow, OCR-bound) contend,
  split into separate queues with independent `MaxWorkers`. Start with one
  queue; split if contention is observed.

- **Gemini model availability.** `gemini-2.5-flash` is the stable model
  supporting `GoogleSearch`. The repo currently uses
  `gemini-3-flash-preview` for chat. If `gemini-3` also supports
  `GoogleSearch`, it can be configured via `--discover-model`. Verify
  during implementation.

- **Socrata API throttling.** Anonymous requests are throttled harder
  than app-token requests. For Astoria+LIC (~12k rows) anonymous access
  should work, but larger areas may need `--nyc-app-token`. The API is
  refreshed daily; cache aggressively (re-pull weekly at most).

- **Bronze layer disk growth.** Like the existing `menutracking` bronze
  layer, there is no rotation/GC. Add a River `PeriodicJob` `bronze-gc`
  (delete > N days) before production — same known gap as menutracking.
  Retry attempts now produce separate files (`{camis}-{attempt}.avro`),
  which compounds growth — the GC job should account for this.

- **`serve` flag defaults differ from `scrape`.** The `scrape` command
  defaults `--llm-url` to `http://localhost:8000/v1`. On `serve`,
  `--llm-url` defaults to `""` so the startup code can detect "not
  configured" and skip building an extractor. If
  `--enable-restaurant-pipeline` is true but `--llm-url` is empty, the
  worker uses a basic `OpenAICompatExtractor` with its own default.

- **Existing menutracking `UniqueOpts` bug.** The existing
  `menutracking_migrate.go:189` uses `ByPeriod: 7 * 24 * time.Hour` without
  `ByArgs`, which dedupes across all sources for the week. Note this but
  don't fix it in this PR — it's a separate concern.

---

## Verification

- **Unit tests:** `make check` passes (lint + test + build).
- **Pipeline refactor:** existing `fodmap scrape <url>` command unchanged
  (calls `pipeline.ExtractMenu` then `pipeline.StoreMenu` — same behavior,
  no new flags required on `scrape` command). `make check` passes after
  step 7 (refactor only, no new features).
- **CSV import:** `fodmap restaurants import --area astoria-lic --limit 10
  --postgres-dsn ...` → 10 rows in DB, 10 discover jobs in River, Avro file
  in `data/bronze/nyc_opendata/{date}/astoria-lic.avro` with `event_id`
  and `created_at` fields (verify `latitude`/`longitude` precision — no
  `float32` corruption).
- **Discovery worker:** run worker, verify a row transitions
  `pending_discovery → url_found` with a real `menu_url`. Verify
  `data/bronze/gemini/{date}/{camis}-1.avro` exists with `event_id`,
  `job_id`, `attempt` fields.
- **Scrape worker:** run worker on a `url_found` row, verify
  `scraped` status + `item_count > 0` + items in Weaviate
  `RestaurantMenu` collection. Verify
  `data/bronze/restaurants/{date}/{camis}-1.html` and `{camis}-1.avro`
  exist with `discovery_event_id` linking back to the Gemini record.
- **Retry:** verify a failed discovery (attempt 1) + retry (attempt 2)
  produces `data/bronze/gemini/{date}/{camis}-1.avro` and
  `{camis}-2.avro` — both preserved.
- **REST API:** `POST /api/v1/restaurants` with a JSON body → row created +
  discover job enqueued. `GET /api/v1/restaurants?status=scraped` → list.
  `POST /api/v1/restaurants/{camis}/retry` → status reset + job enqueued.
- **No regression:** existing `menutracking` pipeline unchanged.

---

## Implementation Order

1. **Migration:** `000002_restaurants.up.sql` — `restaurants` table.
2. **Avro schemas:** Add `NYCRestaurantSchema`, `GeminiDiscoverySchema`,
   `MenuExtractionSchema` to `data/schemas/schemas.go`.
3. **Avro writers:** `menusearch/avro.go` — record builders for each schema.
   Use `ocf.NewEncoder` directly for `nyc_restaurant` (bypass
   `EventWriter.Write` float64→float32 coercion on `double` fields). Use
   `EventWriter` for `gemini_discovery` and `menu_extraction` (no `double`
   fields).
4. **Server interfaces:** `server/restaurant_store.go` — `RestaurantStore`
   + `RiverInserter` interfaces + `Restaurant` struct.
5. **Types + store:** `menusearch/restaurant.go`, `menusearch/store.go` +
   embedded SQL. Store implements `server.RestaurantStore`.
6. **CSV parser:** `menusearch/csv.go` + tests.
7. **Area filter + Socrata client:** `menusearch/areas.go`,
   `menusearch/nycdata.go`.
8. **Refactor:** split `runScrapeWith` from `cli/scrape.go` into
   `pipeline/pipeline.go` as `ExtractMenu` + `StoreMenu` + `ToMenuItems` +
   `ExtractPDF`. Replace `fmt.Printf` with `slog.Info`. Update
   `cli/scrape.go` to call `pipeline.ExtractMenu` then `pipeline.StoreMenu`.
   Verify `make check` passes — no behavior change.
9. **CLI import:** `cli/restaurants.go` — `import --area`, `list`,
   `scrape`, `discover`, `retry` commands.
10. **Discovery worker:** `menusearch/discover.go` + `menusearch/workers.go`
    (job args + `GeminiSearcher` interface + `GeminiMenuSearcher` impl +
    Avro record writing with `{camis}-{attempt}.avro` filename).
11. **Scrape worker:** `menusearch/scrape.go` — calls `ExtractMenu`, writes
    HTML + Avro bronze, then calls `StoreMenu`.
12. **Worker registration:** Merge `menusearch` workers into the existing
    `StartMenutrackingPipeline` (or a new `StartPipelines` that wraps both).
    Add new flags to `serveCmd` (defaults: `--llm-url=""`, not
    `http://localhost:8000/v1`). Wire `RestaurantStore` to `Server` via
    `srv.SetRestaurantStore(...)`. Follow startup ordering: construct →
    register → create client → wire `RiverClient` → `Start`.
13. **REST API:** `server/restaurants_handler.go` — CRUD + trigger + retry
    endpoints.
14. **Docs:** update `README.md`, `docs/guides/cli-reference.md`,
    `docs/guides/data-model.md` (Avro schemas), `start.sh`.
15. **`make check`** passes.