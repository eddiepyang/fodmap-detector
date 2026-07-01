# Plan: Schema consistency + surrogate UUID PK on `restaurants`

**Status:** Draft (2026-06-30). Ready for implementation.

## Why

The database accumulated inconsistencies from incremental migrations:
`restaurants.camis` is a natural-key PK that can't scale past NYC; `business_id`
means different things on different tables (camis on `menu_items`, Yelp UUID
on `reviews`/`conversations`); timestamps mix `TIMESTAMP` and `TIMESTAMPTZ`;
several tables lack `created_at`/`updated_at`; `menu_items.scraped_at_utc` is
`TEXT`; `updated_at` is maintained by hand in SQL strings scattered across the
codebase; and `menu_item_id` collides when the same dish name appears in
different sections.

This plan addresses all of these in one migration (`000010`) by:

1. Adding a **surrogate UUID PK** (`restaurants.id`) and demoting `camis` to a
   nullable `UNIQUE` external ID, with a new `yelp_id` column for Yelp-sourced
   restaurants.
2. Making `business_id` a **UUID FK to `restaurants.id`** on `menu_items`,
   `reviews`, and `conversations` — the universal join key across all tables.
3. **Standardizing all timestamps** to `TIMESTAMPTZ` and adding
   `created_at`/`updated_at` where missing.
4. Adding **DB triggers** (`touch_updated_at()`) on every mutable table so
   `updated_at` is always maintained by the database, not by hand-rolled SQL.
5. **Fixing `menu_item_id` collisions** by including `menu_section` in the
   deterministic key.
6. Converting `menu_items.scraped_at_utc TEXT` → `scraped_at TIMESTAMPTZ`.

The result: every table joins to `restaurants` via `business_id UUID`, all
timestamps are zone-aware and auto-maintained, and the schema is ready for
non-NYC data sources.

## Scope — what changes, what doesn't

| Area | Changes | Stays |
|---|---|---|
| `restaurants` PK | Add `id UUID PK`, demote `camis` to nullable UNIQUE, add `yelp_id` | All other columns; `ON CONFLICT (camis)` upsert path |
| FK columns | `menu_items`/`reviews`/`conversations` `business_id` → UUID FK `restaurants(id)` | Column name `business_id` stays (just the type + FK target changes) |
| Timestamps | All `TIMESTAMP` → `TIMESTAMPTZ`; add `created_at`/`updated_at` to 7 tables | Append-only tables get `created_at` only (no `updated_at`) |
| Triggers | New `touch_updated_at()` function + 8 per-table `BEFORE UPDATE` triggers | None |
| `menu_items.scraped_at_utc` | Rename → `scraped_at`, convert `TEXT` → `TIMESTAMPTZ` | All other menu_items columns |
| `menu_item_id` key | Include `menu_section` in the deterministic UUID | Deterministic/idempotent property |
| Data | TRUNCATE `menu_items`, `reviews`, `review_chunks`, `conversations`, `messages` | `users`, `user_profiles`, `restaurants`, `fodmap_*`, menutracking tables |
| Go structs | `Restaurant.ID`, `Restaurant.YelpID`, `CAMIS` → `*string`; `SearchFilter.BusinessID`/`BusinessResult.ID`/`MenuItem.BusinessID` → `uuid.UUID`; `Conversation.BusinessID` → `uuid.UUID` | Yelp dataset structs (`data/schemas/schemas.go`) stay string (conversion at indexing boundary) |
| SQL queries | `upsert_restaurant.sql` gains `RETURNING id`; new `upsert_restaurant_by_yelp.sql`; new `get_restaurant_by_id.sql`; remove app-side `updated_at = NOW()` | `get_restaurant.sql`/`list_restaurants.sql` (add `id` column only); `get_businesses.sql`/`get_reviews.sql` (column name unchanged) |
| River jobs | `ScrapeMenuArgs.CAMIS` → `RestaurantID uuid.UUID`; `DiscoverMenuArgs.CAMIS` stays | Job kind strings |
| Avro | `MenuExtractionSchema` field `camis` → `business_id` (with alias `["camis"]`); `NYCRestaurantSchema` gains `id` | `GeminiDiscoverySchema` stays `camis` (discovery job input) |
| HTTP API | Keep `{camis}` path param; `business_id` query params now accept UUID strings | Route structure |
| CLI | `--camis` flag stays (user-facing); internal resolution to UUID | All other flags |
| Frontend | Must send UUID (not camis) as `business_id` in conversation create / search filter | All other frontend behavior |

## Architecture

### The universal join key

```
                    restaurants.id (UUID PK)
                   /         |          \
     menu_items.business_id  |   conversations.business_id
                   reviews.business_id (nullable FK)
```

All four tables reference `restaurants.id` via `business_id UUID`. A single
join key works across the entire schema:

```sql
SELECT r.dba, m.dish_name, rv.text, c.title
FROM restaurants r
JOIN menu_items  m  ON m.business_id  = r.id
JOIN reviews     rv ON rv.business_id = r.id
JOIN conversations c ON c.business_id  = r.id
WHERE r.id = $1;
```

### External IDs

`restaurants` carries two nullable external ID columns for source-native
lookups:

| Column | Source | Use |
|---|---|---|
| `camis TEXT UNIQUE` | NYC DOHMH OpenData | NYC ingestion upserts on this; HTTP `{camis}` lookups |
| `yelp_id TEXT UNIQUE` | Yelp dataset | Yelp ingestion upserts on this; cross-source dedup |

Future sources add their own `*_id` columns. The surrogate `id` UUID is the
internal identity; external IDs are lookup keys.

### `updated_at` maintenance

A shared trigger function is installed on every **mutable** table:

```sql
CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger AS $$
BEGIN NEW.updated_at = NOW(); RETURN NEW; END;
$$ LANGUAGE plpgsql;
```

Each mutable table gets a `BEFORE UPDATE` trigger calling it. App code no
longer sets `updated_at = NOW()` in SQL strings — the trigger owns it. The
one exception is `AddMessage`'s conversation bump, which uses explicit
`SET updated_at = NOW()` to fire the trigger and document the intent.

### Data loss

Five tables are TRUNCATE'd because their `business_id` columns hold string
values (camis or Yelp IDs) that can't satisfy the new UUID FK:

| Table | Why TRUNCATE | Regenerable? |
|---|---|---|
| `menu_items` | `business_id` holds camis strings | Yes — re-scrape |
| `reviews` | `business_id` holds Yelp string IDs | Yes — re-index from JSONL archive |
| `review_chunks` | Cascade from `reviews` TRUNCATE | Yes — re-index |
| `conversations` | `business_id` holds mixed strings | No — prototype chat history lost (accepted) |
| `messages` | Cascade from `conversations` TRUNCATE | No — prototype chat history lost (accepted) |

## Migration DDL (`000010_schema_consistency.up.sql`)

```sql
-- ═══ restaurants: surrogate UUID PK + external IDs ═══
ALTER TABLE restaurants ADD COLUMN id UUID PRIMARY KEY DEFAULT gen_random_uuid();
ALTER TABLE restaurants ALTER COLUMN camis DROP NOT NULL;
ALTER TABLE restaurants ADD CONSTRAINT restaurants_camis_unique UNIQUE (camis);
ALTER TABLE restaurants ADD COLUMN yelp_id TEXT UNIQUE;

-- ═══ menu_items: FK → UUID, TRUNCATE, rename scraped_at, add timestamps ═══
ALTER TABLE menu_items DROP CONSTRAINT IF EXISTS menu_items_business_id_fkey;
TRUNCATE TABLE menu_items;
ALTER TABLE menu_items DROP COLUMN business_id;
ALTER TABLE menu_items ADD COLUMN business_id UUID NOT NULL;
ALTER TABLE menu_items ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE menu_items ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE menu_items RENAME COLUMN scraped_at_utc TO scraped_at;
ALTER TABLE menu_items ALTER COLUMN scraped_at TYPE TIMESTAMPTZ USING scraped_at::timestamptz;
ALTER TABLE menu_items
    ADD CONSTRAINT menu_items_business_id_fkey
    FOREIGN KEY (business_id) REFERENCES restaurants(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_menu_items_business_id ON menu_items(business_id);

-- ═══ reviews: FK → UUID, TRUNCATE, add created_at, add index ═══
TRUNCATE TABLE reviews CASCADE;
ALTER TABLE reviews DROP COLUMN business_id;
ALTER TABLE reviews ADD COLUMN business_id UUID REFERENCES restaurants(id) ON DELETE CASCADE;
ALTER TABLE reviews ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX IF NOT EXISTS idx_reviews_business_id ON reviews(business_id);

-- ═══ conversations: FK → UUID, TRUNCATE, convert timestamps ═══
TRUNCATE TABLE conversations CASCADE;
ALTER TABLE conversations DROP COLUMN business_id;
ALTER TABLE conversations ADD COLUMN business_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE;
ALTER TABLE conversations ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'UTC';
ALTER TABLE conversations ALTER COLUMN updated_at TYPE TIMESTAMPTZ USING updated_at AT TIME ZONE 'UTC';

-- ═══ users: add updated_at, convert created_at ═══
ALTER TABLE users ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE users ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'UTC';

-- ═══ user_profiles: convert timestamps ═══
ALTER TABLE user_profiles ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'UTC';
ALTER TABLE user_profiles ALTER COLUMN updated_at TYPE TIMESTAMPTZ USING updated_at AT TIME ZONE 'UTC';

-- ═══ messages: convert created_at ═══
ALTER TABLE messages ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'UTC';

-- ═══ extraction_rules: add updated_at ═══
ALTER TABLE extraction_rules ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- ═══ fodmap_catalog: add created_at, convert updated_at ═══
ALTER TABLE fodmap_catalog ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE fodmap_catalog ALTER COLUMN updated_at TYPE TIMESTAMPTZ USING updated_at AT TIME ZONE 'UTC';

-- ═══ review_chunks: add created_at ═══
ALTER TABLE review_chunks ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- ═══ fodmap_ingredients: add created_at ═══
ALTER TABLE fodmap_ingredients ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- ═══ Trigger function (shared) ═══
CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ═══ Per-table BEFORE UPDATE triggers on mutable tables ═══
CREATE TRIGGER trg_users_updated_at            BEFORE UPDATE ON users           FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_user_profiles_updated_at   BEFORE UPDATE ON user_profiles  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_conversations_updated_at   BEFORE UPDATE ON conversations  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_restaurants_updated_at     BEFORE UPDATE ON restaurants    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_sources_updated_at         BEFORE UPDATE ON sources        FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_extraction_rules_updated_at BEFORE UPDATE ON extraction_rules FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_menu_items_updated_at      BEFORE UPDATE ON menu_items     FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_fodmap_catalog_updated_at  BEFORE UPDATE ON fodmap_catalog  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
```

The down migration reverses everything: drop triggers → drop function →
drop added columns → revert type conversions → revert `scraped_at` →
`scraped_at_utc TEXT` → drop FKs → drop `restaurants.id`/`yelp_id`/
`restaurants_camis_unique` → restore `restaurants.camis` as NOT NULL PK →
restore FK columns as `TEXT`.

## Go code changes

### `server/restaurant_store.go` — struct + interface

- `Restaurant`: add `ID uuid.UUID \`json:"id"\`` (first field); `CAMIS` `string` → `*string`; add `YelpID *string \`json:"yelp_id,omitempty"\``.
- `Upsert(ctx, r Restaurant) error` → `Upsert(ctx, r Restaurant) (*Restaurant, error)` — SQL gains `RETURNING id`.
- New: `GetByID(ctx, id uuid.UUID) (*Restaurant, error)` — for call sites that already have the UUID.

### `menusearch/store.go` — implementations

- `Upsert` returns `(*Restaurant, error)` — scans `RETURNING id`.
- `Get`/`List` — SELECT now includes `id`, `yelp_id`; scan populates `r.ID`, `r.YelpID`.
- New: `GetByID` — embeds new `get_restaurant_by_id.sql`.

### `menusearch/store/sql/*.sql`

- `upsert_restaurant.sql`: add `RETURNING id`; remove `updated_at = NOW()` (trigger owns it).
- New `upsert_restaurant_by_yelp.sql`: `ON CONFLICT (yelp_id) DO UPDATE ... RETURNING id` — for Yelp ingestion union step.
- New `get_restaurant_by_id.sql`: `WHERE id = $1`.
- `get_restaurant.sql`/`list_restaurants.sql`: add `id`, `yelp_id` to SELECT.
- `update_discovery_urls.sql`/`update_scrape_result.sql`/`set_extraction_tier.sql`: remove `updated_at = NOW()` (trigger owns it).

### `pipeline/pipeline.go` — scrape pipeline

- `StoreMenu`/`ToMenuItems`: `camis string` → `restaurantID uuid.UUID`.
- `menu_item_id` key: `restaurantID.String() + "|" + section + "|" + dishName` (collision fix).

### `menusearch/scrape.go` — scrape worker

- `storeAndFinish`: uses `rest.ID` (already fetched for address enrichment) as `restaurantID`.
- `ScrapeMenuArgs.CAMIS string` → `RestaurantID uuid.UUID \`json:"restaurant_id"\``.

### `menusearch/discover.go` — discovery worker

- `enqueueScrapeJobs` (line 248): pass `RestaurantID: rest.ID` (resolved UUID), not `args.CAMIS` (string).
- The worker already calls `w.Store.Get(ctx, args.CAMIS)` — reuse `rest.ID`.

### `menusearch/job_queue.go` — enqueue paths

- `EnqueueScrape`: `ScrapeMenuArgs{RestaurantID: r.ID, ...}`.
- `EnqueueDiscover`: `DiscoverMenuURLArgs{CAMIS: safeDeref(r.CAMIS), ...}` — stays camis-keyed (discovery is triggered by camis from NYC OpenData).

### `cli/scrape.go` — scrape command

- Add restaurant store dependency (`--postgres-dsn`).
- `--camis` flag stays (user-facing).
- Resolve: `restaurantStore.Get(ctx, camis)` → `rest.ID` → pass to `StoreMenu`.
- If `rest == nil`, auto-create a restaurants row via `Upsert` and use returned `ID`.

### `cli/restaurants.go` — CLI commands + Avro replay

- Scrape/discover/retry commands: `camis := args[0]` stays; `store.Get` returns `rest.ID`.
- NYC CSV ingestion: `Upsert(ctx, Restaurant{CAMIS: &rec.CAMIS, ...})` — `CAMIS` is now `*string`.
- Avro replay: check both `record["business_id"]` and `record["camis"]` (backward compat with old Avro files).

### `search/weaviate.go` — search structs + Weaviate boundary

- `MenuItem.BusinessID`, `SearchFilter.BusinessID`, `BusinessResult.ID`: `string` → `uuid.UUID`.
- Write sites: `.String()` to convert UUID → string for Weaviate property.
- Read sites: `uuid.Parse()` to convert string → UUID from Weaviate metadata.

### `search/pinecone.go` — Pinecone boundary

Same pattern as Weaviate: `.String()` on write, `uuid.Parse()` on read.

### `search/postgres.go` — Postgres upserts + queries

- Reviews upsert: `business_id` now UUID; add `created_at` to INSERT.
- Menu items upsert: add `created_at, updated_at` to INSERT; remove `updated_at` from `ON CONFLICT DO UPDATE SET` (trigger owns it); rename `scraped_at_utc` → `scraped_at`.
- SearchMenu SELECT: `scraped_at_utc` → `scraped_at`.
- Reviews WHERE filter: bind `filter.BusinessID` (uuid.UUID).

### `auth/conversation.go` + `auth/postgres_store.go`

- `Conversation.BusinessID` → `uuid.UUID`.
- INSERT/SELECT: pgx handles UUID natively.
- `user_profiles` upsert: remove `updated_at = CURRENT_TIMESTAMP` (trigger owns it).
- `AddMessage`: `UPDATE conversations SET updated_at = NOW() WHERE id = $1` (explicit, readable; trigger overwrites anyway).

### `server/create_conversation.go` — conversation creation

- `createConversationRequest.BusinessID`: accept UUID string from API, parse with `uuid.Parse()`, 400 on parse error.
- `search.SearchFilter{BusinessID: businessID}` — `BusinessID` is now `uuid.UUID`.

### `server/chat_handler.go` — chat context reload

- Remove the `"general"` sentinel check (line 171) — dead code, nothing sets `BusinessID = "general"`.
- Remove the unreachable "general assistant mode" branch (lines 220-235).
- `conv.BusinessID` is now `uuid.UUID`; `search.SearchFilter{BusinessID: conv.BusinessID}`.

### `server/handlers.go` — reviews API

- `getReviewsHandler`: parse `business_id` query param as UUID, 400 on parse error.
- `reviewsHandler` (legacy archive-backed): stays string-based (reads from JSONL archive, not DB). Document the polymorphic `business_id` query param or deprecate this endpoint.

### `cli/index.go` — Yelp ingestion union step

- For each distinct Yelp `business_id`: upsert a `restaurants` row with `yelp_id` populated → get `id` (UUID). Build `yelpToUUID map[string]uuid.UUID`.
- For each review: look up `yelpToUUID[r.BusinessID]`. If found, set `reviews.business_id = uuid`. If not found, set `reviews.business_id = NULL` (nullable FK allows it).
- `data/schemas/schemas.go` `Review.BusinessID`/`Business.BusinessID` stay `string` (external Yelp field; conversion at indexing boundary).

### Avro schema + writer/reader

- `data/schemas/schemas.go`:
  - `MenuExtractionSchema`: `"camis"` → `"business_id"` with `"aliases": ["camis"]`.
  - `NYCRestaurantSchema`: add `"id"` field (UUID as string, default `""`).
  - `GeminiDiscoverySchema`: stays `"camis"` (discovery job input).
- `menusearch/avro.go`:
  - `MenuExtractionRecord.CAMIS` → `BusinessID string` (UUID string).
  - `WriteMenuExtractionAvro`: writes `"business_id": rec.BusinessID`.
  - `WriteNYCRestaurantAvro`: add `"id": rec.ID`.
- `scripts/avro_tier_check/main.go`: read both `oldRec["business_id"]` and `oldRec["camis"]` (backward compat).

### River job args

- `ScrapeMenuArgs.CAMIS` → `RestaurantID uuid.UUID \`json:"restaurant_id"\``.
- `DiscoverMenuArgs.CAMIS` stays `string \`json:"camis"\``.

### Remove app-side `updated_at` writes

All handled by the trigger:
- `menusearch/store/sql/upsert_restaurant.sql` — remove `updated_at = NOW()`.
- `menusearch/store/sql/update_discovery_urls.sql` — remove.
- `menusearch/store/sql/update_scrape_result.sql` — remove.
- `menusearch/store/sql/set_extraction_tier.sql` — remove.
- `menutracking/store/sql/insert_source.sql` — remove `updated_at = EXCLUDED.updated_at`.
- `auth/postgres_store.go` — remove `updated_at = CURRENT_TIMESTAMP` from user_profiles upsert.

### `"general"` sentinel elimination

`conversations.business_id` is now `UUID NOT NULL` with FK — the string `"general"` can't exist. Remove:
- `chat_handler.go:171` — the `conv.BusinessID != "general"` check → `conv.BusinessID != uuid.Nil`.
- `chat_handler.go:220-235` — the unreachable "general assistant mode" dead code branch.

## Deployment runbook

1. **Drain River queue**: stop all workers, wait for in-flight jobs to complete. In-flight `ScrapeMenuArgs` with `{"camis":"..."}` won't deserialize to `{"restaurant_id":...}` after the code change.
2. **Apply migration**: `go run . db migrate-up` (applies 000010).
3. **Deploy new code**: the new binary expects the new schema.
4. **Re-index Yelp reviews** (if needed): `go run . index --postgres-search --postgres-dsn ...` — runs the union step (creates restaurants rows from Yelp business_ids, links reviews).
5. **Re-scrape restaurants** (if needed): `go run . restaurants retry-failed` — re-populates menu_items with UUID business_ids.
6. Start workers.

## Test updates

- All test `business_id` values: string literals → `uuid.MustParse("...")` or `uuid.New()`.
- `server/restaurants_handler_test.go`: `stubRestaurantStore` gains `GetByID`; `Upsert` returns `(*Restaurant, error)`. Path params stay `{camis}`.
- `auth/postgres_store_test.go`: sqlmock column lists stay `business_id` (column name unchanged); scan targets change to `uuid.UUID`. Update `ExpectExec` for `AddMessage` SQL change.
- `cli/scrape_service_test.go`: `runScrapeWith` param `camis string` → `restaurantID uuid.UUID`; 14 callers updated.
- `pipeline/pipeline_extract_test.go`: `"test-camis"` → UUID (11+4 sites).
- `menusearch/jobs_test.go`: `ScrapeMenuArgs.CAMIS` → `RestaurantID` (UUID); `DiscoverMenuArgs.CAMIS` stays.
- `search/postgres_test.go`, `search/weaviate_test.go`, `search/pinecone_test.go`: business_id values → UUIDs.
- `internal/db/migrate_integration_test.go`: add assertions for new schema shape (column types, FK existence, trigger existence).

## Docs updates

- `docs/guides/database-schema.md`: regenerate all table blocks for new columns, types, FKs, triggers.
- `docs/guides/scraping-pipeline-cli.md`, `docs/guides/scrape-diagnostics.md`: SELECT examples add `id` column.
- `docs/guides/troubleshooting.md`: split `args->>'camis'` examples for discover jobs (camis) vs scrape jobs (restaurant_id).

## Risks and Gaps

| # | Risk | Severity | Mitigation |
|---|---|---|---|
| 1 | In-flight River jobs can't deserialize after `ScrapeMenuArgs` field rename | HIGH | Drain queue before deploying (runbook step 1) |
| 2 | `Upsert` must return `id` — callers need the UUID | HIGH | Change interface + SQL to `RETURNING id` |
| 3 | Yelp ingestion needs a second upsert path (`ON CONFLICT (yelp_id)`) | MEDIUM | New `upsert_restaurant_by_yelp.sql` |
| 4 | Reviews without a matching business map entry → `business_id = NULL` | MEDIUM | Indexer handles nil case explicitly |
| 5 | HTTP `business_id` query params must parse UUID, 400 on error | MEDIUM | Add `uuid.Parse` + error handling at every entry point |
| 6 | Weaviate/Pinecone store UUIDs as strings — ~15 boundary conversions | LOW (high churn) | Mechanical `.String()` / `uuid.Parse()` at each read/write site |
| 7 | Old Avro files use `"camis"` key — `map[string]any` readers need dual-key check | MEDIUM | Check both `record["business_id"]` and `record["camis"]` |
| 8 | `AddMessage` conversation bump must fire the trigger cleanly | LOW | Use explicit `SET updated_at = NOW()` (not `SET id = id` hack) |
| 9 | Call sites with UUID should use `GetByID`, not `Get(camis)` | LOW | Add `GetByID`; use in chat handler + scrape worker |
| 10 | `troubleshooting.md` references `args->>'camis'` for scrape jobs | LOW | Split examples for discover (camis) vs scrape (restaurant_id) |
| 11 | Migration integration test doesn't assert column types/constraints | MEDIUM | Add schema-shape assertions after migration |
| 12 | `discover.go:248` must pass `rest.ID` to `ScrapeMenuArgs`, not `args.CAMIS` | HIGH | Explicit call-site fix in the plan |
| 13 | Frontend must send UUID as `business_id` in conversation create / search | MEDIUM | Coordinated frontend+backend deploy |
| 14 | `reviewsHandler` (legacy archive endpoint) stays string-based — polymorphic `business_id` param | LOW | Document or deprecate |

## Verification

```
make test
```

Runs `golangci-lint run ./...` then `go test ./... -count=1 -v`. Key tests:
- `internal/db/migrate_integration_test.go` — migration applies + new schema assertions
- `server/restaurants_handler_test.go` — handlers with UUID-aware stubs
- `auth/postgres_store_test.go` — updated SQL expectations
- `menusearch/scrape_test.go` — Avro roundtrip with full fields + UUID
- `pipeline/pipeline_extract_test.go` — `ToMenuItems`/`StoreMenu` with UUID
- `search/postgres_test.go` — UUID business_id in upserts/queries
- `search/weaviate_test.go` — UUID at Weaviate boundary