# Split storage: chunks in Weaviate, full reviews in Postgres

## Context

Weaviate is logging warnings on every city-filtered chat query:

```
Number of found nested reference results exceeds configured QUERY_MAXIMUM_RESULTS.
nested_reference_results: 40057  query_maximum_results: 10000
```

Root cause: `search/weaviate.go:GetBusinesses` queries the `YelpReviewChunk` class and filters by `hasParent → YelpReview → city = "Philadelphia"`. Weaviate expands every matching parent (40,057 Philadelphia reviews, 65,658 for another city), exceeding the default 10,000 cap. Risk: perf degradation and silent truncation of the candidate set before the hybrid/nearVector ranking step — search-quality loss the user can't see.

Two structural facts shape the fix:

1. Weaviate's docs ([Best Practices](https://docs.weaviate.io/weaviate/best-practices)) explicitly call cross-refs a perf footgun and recommend denormalization. Pinecone has no cross-refs at all — [search/pinecone.go](search/pinecone.go) already flattens these same fields into vector metadata. The Weaviate path is the outlier.
2. A Postgres `reviews` table with the right schema already exists at [search/postgres.go:68-77](search/postgres.go#L68-L77) (`review_id PK, business_id, business_name, city, state, categories, stars, text`), with a matching upsert at [postgres.go:118-129](search/postgres.go#L118-L129). Today it's populated only when `STORE_TYPE=postgres` is the search backend; this plan promotes it to canonical review storage that's always written.

The change denormalizes hot-path fields onto `YelpReviewChunk`, stops writing the `YelpReview` parent to Weaviate, and routes every review write to Postgres regardless of search backend. Clean separation: Weaviate = vector index, Postgres = source of truth for structured data.

Migration is wipe-and-rebuild rather than in-place. The two Weaviate classes that hold Yelp data (`YelpReview`, `YelpReviewChunk`) get deleted; the server restart re-creates `YelpReviewChunk` with the new schema; the Postgres `reviews` table is created by `golang-migrate` via `go run . db migrate-up` (not by `EnsureSchema`); the indexer re-runs from offset 0 against the existing Yelp archive at `/Users/edwardyang/projects/data/yelp_dataset.tar`. Avoids a bespoke migration CLI in exchange for ~3 hours of re-embed time on the dev box.

## Tradeoffs

All tradeoffs below compare the current plan (split storage: chunks in Weaviate, reviews in Postgres) against the rejected alternative — keeping `YelpReview` as a Weaviate cold-storage class with a deterministic-UUID lookup helper (the "two Weaviate classes" variant). Migration mechanics (wipe-and-rebuild vs. multi-pass) is the same under both variants and not a tradeoff here — we picked wipe in either case, trading ~3 hours of re-embed clock time for ~2 days of avoided migration-CLI engineering time.

**1. Dev cost ↑ for architectural cleanliness ↑.** Promoting Postgres to canonical review storage adds a `ReviewStore` interface, dual-write coordination, and ~half a day of work, vs. keeping `YelpReview` in Weaviate as cold storage with a deterministic-UUID fetch helper (~2 hours). In exchange: Weaviate becomes a pure vector index, Postgres is the structured-data source of truth, and review text isn't trapped in whichever vector backend happens to be active.

**2. Indexer infra surface ↑ for backend independence ↑.** Postgres becomes a hard dependency of the indexer (already mandatory for the running server — auth, conversations, menutracking pipeline). The indexer dual-writes (reviews → PG, chunks → vector store), which adds one more failure mode per batch — mitigated by idempotent upserts on both sides (PG `ON CONFLICT DO UPDATE`, Weaviate deterministic UUIDs) so retries are safe. The rejected variant required no PG dependency in the indexer.

**What's not a tradeoff** (both variants get these): the `QUERY_MAXIMUM_RESULTS` warning goes away; chat result quality stops being silently truncated; the where-filter becomes a direct-property lookup, faster than the cross-ref path; the codebase converges on the canonical vector-DB pattern, matching how `pinecone.go` already works.

## Bonus scope

**Fix FodmapIngredient schema drift.** The Weaviate `FodmapIngredient` class was auto-generated from data, declaring `substitutions` as `text` while code now sends `[]string`. This produces hundreds of WARN log lines on every server startup. The wipe is essentially free for this class (120 items, re-upserted from source on every server start), so we declare it explicitly with `substitutions: text[]` in the same EnsureSchema pass. Bundled because re-doing it later wastes work.

## Approach

### 1. Weaviate schema — `search/weaviate.go:148-199` (`EnsureSchema`)

Two changes:

**1a. `YelpReviewChunk`**: declare 7 properties alongside `chunkText`:

| Property | Type | Used for |
|---|---|---|
| `city` | text, filterable | where-filter |
| `state` | text, filterable | where-filter |
| `categories` | text, filterable + searchable (Like) | where-filter |
| `businessId` | text, filterable | where-filter |
| `reviewId` | text, filterable | where-filter + PG key |
| `businessName` | text, filterable | response payload |
| `stars` | number | response payload |

Match tokenization/indexing of the equivalents currently on `YelpReview` (lines 157–173) so behavior is identical post-migration. Remove the `hasParent` cross-ref property — new schema doesn't have it.

**1b. `FodmapIngredient`**: no code change needed — `EnsureFodmapSchema` at [weaviate.go:750-772](search/weaviate.go#L750-L772) already declares `substitutions` as `text[]`. The runtime bug is that the existing Weaviate class was auto-created with `text` before this code was added, and `EnsureFodmapSchema` exits early when the class already exists. The wipe-and-rebuild in step 8 deletes the stale class so it gets re-created with the correct `text[]` type.

Remove the `YelpReview` class declaration from `EnsureSchema` entirely — it's no longer created on fresh installs.

### 2. Postgres schema — `search/postgres.go:65-101` (`EnsureSchema`)

> **Note:** `EnsureSchema` on PostgresClient is now a no-op. The `reviews` table is created by `golang-migrate` via `go run . db migrate-up` (see `internal/db/migrations/`). The call to `EnsureSchema` still exists in the server bootstrap but does nothing.

Already declares the `reviews` table (in the migration, not in EnsureSchema). Change: ensure the table is created unconditionally whenever `POSTGRES_DSN` is set, not only when `STORE_TYPE=postgres`. This is now handled by `db migrate-up`. Wire the call into the server bootstrap and the indexer's startup so the table is guaranteed to exist before either reads or writes it.

The orphan `review_chunks` and `fodmap_ingredients` tables created by the same EnsureSchema are harmless empty tables when WV is the search backend — leave them alone, no cleanup needed.

### 3. `ReviewStore` interface + dual-write — new

Add in `search/review_store.go`:

```go
type ReviewStore interface {
    UpsertReviews(ctx context.Context, items []IndexItem) error
}
```

Implementations:
- `PostgresClient.UpsertReviews` — extract the review-only branch of [postgres.go:118-129](search/postgres.go#L118-L129) into a public method.
- No-op impl (returned when search backend is itself Postgres so we don't double-write).

The indexer accepts a `ReviewStore` alongside its search backend and calls `reviewStore.UpsertReviews(items)` before `searchBackend.BatchUpsert(items)` per batch. Idempotent on both sides — partial-batch failures retry safely via the existing `index.checkpoint`.

Connection-pool sizing: set `db.SetMaxOpenConns(25)` and `SetMaxIdleConns(10)` on the `PostgresClient`'s sql.DB to absorb dual-write at indexer throughput. The indexer's existing batch size (~500) × concurrent workers shouldn't exceed this.

Wire selection in `cli/index.go` by `--store-type`:
- `weaviate` (default): search = Weaviate, reviews = Postgres.
- `postgres`: search = Postgres, reviews = no-op (chunks+reviews already write together).
- `pinecone`: search = Pinecone, reviews = Postgres.

### 4. Weaviate `BatchUpsert` — `search/weaviate.go:201-304`

Two changes:

1. Extend the chunk property map at lines 263–266 with the 7 new fields. All values already populated on `IndexItem` from [cli/index.go:184-196](cli/index.go#L184-L196).
2. Delete the parent batch step (lines 207–236) and the cross-reference batch step (lines 282–303). The Weaviate client stops writing `YelpReview` entirely.

### 5. Search where-filter — `search/weaviate.go:459-517` (`buildWhereFilter`)

Replace each cross-ref path with a direct property path:

- `["hasParent", "YelpReview", "categories"]` → `["categories"]`
- `["hasParent", "YelpReview", "city"]` → `["city"]`
- `["hasParent", "YelpReview", "state"]` → `["state"]`
- `["hasParent", "YelpReview", "businessId"]` → `["businessId"]`
- `["hasParent", "YelpReview", "reviewId"]` → `["reviewId"]`

Operators unchanged. Both `GetBusinesses` and `GetReviews` call this helper, so both queries fix in one edit.

### 6. Response assembly — `aggregateTopK` and `extractParent`

`GetBusinesses` ([weaviate.go:314-327](search/weaviate.go#L314-L327)) and `GetReviews` ([weaviate.go:397-405](search/weaviate.go#L397-L405)) currently select `hasParent { ... on YelpReview { ... } }`. Replace those sub-selections with flat field reads off the chunk: `reviewId, businessId, businessName, city, state, categories, stars`.

`GetReviews` currently fetches `text` from the parent YelpReview to return full review text. After denormalization, `text` is **not** stored on the chunk — only `chunkText` (a partial excerpt) lives there. Rather than duplicating full review text across every chunk (3-5× storage overhead per review), `GetReviews` now calls `PostgresClient.GetReviewByID` for each distinct `reviewId` returned by Weaviate to retrieve the full `text`. This is a net performance win: a single PK lookup on an existing Postgres connection takes microseconds, compared to the current cross-ref expansion that resolves all 40k+ matching parents in Weaviate before even reaching the ranking step. The Weaviate query only returns top-K chunk hits (~10-20), so at most ~10-20 PG rows fetched — well within the existing connection pool.

`extractParent` ([weaviate.go:1167-1188](search/weaviate.go#L1167-L1188)) becomes a flat field extractor — simplify or inline at call sites if it shrinks to one line.

### 7. Full-review retrieval helper — Postgres

Add `(*PostgresClient).GetReviewByID(ctx, reviewID) (*Review, error)` in `search/postgres.go`. SQL in a new `search/sql/get_review_by_id.sql`:

```sql
SELECT review_id, business_id, business_name, city, state, categories, stars, text
FROM reviews
WHERE review_id = $1;
```

Single-row PK lookup on an indexed primary key (~microsecond latency on an existing connection). Called by `GetReviews` after the Weaviate query returns top-K chunks to hydrate full review text. Also serves as an escape hatch for any future "show full review" UI, debugging, or reranking-with-full-text.

### 8. Wipe and rebuild — surgical

**Scope of the wipe**:

| Store | Preserve | Delete |
|---|---|---|
| Postgres | `users`, `conversations`, `messages`, `user_profiles`, `sources`, `extraction_rules`, `regulatory_updates`, `menutracking_dead_letter`, `river_*` | nothing — `reviews` is created by `go run . db migrate-up` |
| Weaviate | `RestaurantMenu` (110 scraped menus — losing these forces a full re-scrape) | `YelpReview`, `YelpReviewChunk`, `FodmapIngredient` (re-upserted from source on server start, fixes the schema drift) |

**Run order**:

1. **Stop the in-flight indexer.** `pkill -f "go run . index"` (or kill the specific PID); confirm with `ps aux | grep "go run . index"` returns empty.
2. **Delete the three Weaviate classes**:
   ```
   curl -X DELETE http://localhost:8090/v1/schema/YelpReview
   curl -X DELETE http://localhost:8090/v1/schema/YelpReviewChunk
   curl -X DELETE http://localhost:8090/v1/schema/FodmapIngredient
   ```
3. **Restart the Go server.** `EnsureSchema` recreates `YelpReviewChunk` with the new 7-field schema and `FodmapIngredient` with the explicit `text[]` shape; Postgres tables are created by `go run . db migrate-up`; server startup re-upserts the 120 FODMAP ingredients into the fresh class.
4. **Run a fresh index pass.** `go run . index --weaviate localhost:8090` from offset 0 (the prior `index.checkpoint` references the wiped class — delete or rename `index.checkpoint` first). Re-embeds 296k reviews ≈ ~3 hours at observed ~180 chunks/sec, dual-writing reviews to PG and chunks to WV. Chat returns "no businesses found" for cities until enough chunks land — acceptable for a dev box.

**Do not run**: `docker compose down -v` (wipes the WV data volume *and* the PG data volume, destroying auth/chat/menutracking), `DROP DATABASE fodmap` (same), `rm -rf` on the Yelp archive at `/Users/edwardyang/projects/data/yelp_dataset.tar`.

### 9. Tests

- `search/weaviate_test.go` — update `makeData()` ([lines 21-48](search/weaviate_test.go#L21-L48)) to put the 7 fields directly on chunks (no parent cross-ref). Existing `GetBusinesses`/`GetReviews` filter assertions fail-loud if any where-path string is wrong.
- `search/postgres_test.go` — add `GetReviewByID` and `UpsertReviews` tests against a real Postgres (follow the existing pattern from `integration/handlers_test.go`).
- `search/review_store_test.go` (new) — no-op impl + interface compliance.
- `cli/index_test.go` — new test that the indexer calls `ReviewStore.UpsertReviews` exactly once per batch and stops on either side's error.

## Files modified

- `/Users/edwardyang/projects/fodmap-detector/search/weaviate.go` — drop `YelpReview` class declaration, drop `hasParent` cross-ref write in `BatchUpsert`, add 7 chunk props, `buildWhereFilter` direct paths, `GetBusinesses`/`GetReviews` field selection (flat reads off chunk + PG `GetReviewByID` call for `text` in `GetReviews`), `extractParent` simplification.
- `/Users/edwardyang/projects/fodmap-detector/search/postgres.go` — extract `UpsertReviews` method satisfying `ReviewStore`, add `GetReviewByID`, set pool sizing.
- `/Users/edwardyang/projects/fodmap-detector/search/review_store.go` (new) — `ReviewStore` interface + no-op impl.
- `/Users/edwardyang/projects/fodmap-detector/search/sql/get_review_by_id.sql` (new).
- `/Users/edwardyang/projects/fodmap-detector/search/weaviate_test.go` — mock chunk shape.
- `/Users/edwardyang/projects/fodmap-detector/search/postgres_test.go` — `GetReviewByID` + `UpsertReviews` tests.
- `/Users/edwardyang/projects/fodmap-detector/search/review_store_test.go` (new).
- `/Users/edwardyang/projects/fodmap-detector/cli/index.go` — accept and call `ReviewStore` alongside the search backend, wire selection by `--store-type`.
- `/Users/edwardyang/projects/fodmap-detector/cli/index_test.go` — dual-write assertions.
- `/Users/edwardyang/projects/fodmap-detector/server/server.go` — call `PostgresClient.EnsureSchema()` at startup whenever `POSTGRES_DSN` is set (now a no-op — table creation handled by `go run . db migrate-up`).

Not modified: `docker-compose.yaml` (no cap bump needed), `pinecone.go` (already flat), `cli/index_backfill.go` / `cli/index_migrate.go` (NOT NEEDED — wipe-and-rebuild replaces both).

## Verification

1. **Schema after restart**:
   - `curl http://localhost:8090/v1/schema/YelpReviewChunk | jq '.properties[].name'` shows the 7 new properties and no `hasParent`.
   - `curl http://localhost:8090/v1/schema/YelpReview` returns 404.
   - `curl http://localhost:8090/v1/schema/FodmapIngredient | jq '.properties[] | select(.name=="substitutions")'` shows `dataType: ["text[]"]`.
   - `psql "$POSTGRES_DSN" -c "\d reviews"` shows the table.
   - Server log no longer floods with `invalid text property 'substitutions'` warnings.

2. **Dual-write smoke (early in reindex)**: after the indexer has written its first ~10 batches:
   - `SELECT COUNT(*) FROM reviews;` returns a non-zero number matching the indexer log's per-batch count.
   - Weaviate `Aggregate { YelpReviewChunk { meta { count } } }` returns a matching multiple (chunks-per-review).
   - Pick a `reviewId` from PG, query it in WV — confirms both sides got the same data.

3. **Filter on direct field**:
   ```graphql
   { Aggregate { YelpReviewChunk(where:{path:["city"],operator:Equal,valueText:"Philadelphia"}) { meta { count } } } }
   ```
   Returns non-zero as soon as Philadelphia reviews are indexed.

4. **End-to-end chat / warning gone**: with backfilled Philadelphia chunks (typically within the first ~10 minutes of reindex), `POST /api/v1/chat/noodles {"message":"low fodmap noodles","city":"Philadelphia","state":"PA"}` returns actual businesses. `docker compose logs weaviate --since 30s` shows **no** `QUERY_MAXIMUM_RESULTS` warning lines.

5. **Full-review escape hatch**: `psql "$POSTGRES_DSN" -c "SELECT text FROM reviews WHERE review_id = (SELECT review_id FROM reviews LIMIT 1);"` returns the text. Confirms `GetReviewByID` SQL path.

6. **Preserved data intact**: `SELECT COUNT(*) FROM users;`, `SELECT COUNT(*) FROM conversations;`, `SELECT COUNT(*) FROM sources;` all return their pre-wipe counts. `Aggregate { RestaurantMenu { meta { count } } }` returns 110.

7. **Unit tests**: `cd /Users/edwardyang/projects/fodmap-detector && make check` passes.

8. **Latency** (optional): time the same chat request before and after migration on a city with >10k reviews — expect a measurable drop.
