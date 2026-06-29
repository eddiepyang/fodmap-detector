# Plan: River schema isolation + dual-write menu stores + TEI embedder

**Status:** Draft (2026-06-29). Ready for implementation.

## Why

Three related changes, all in the durable spine (this Go repo — the Python
`scraper` service stays stateless and untouched):

1. **River tables → `river` schema.** River's internal tables (`river_job`,
   `river_leader`, `river_queue`, `river_client`) are created by
   `river migrate-up` and currently land in the default `public` schema.
   `docs/plans/regtrack-pipeline-plan.md` already states the intent to isolate
   them in a dedicated `river` schema; this plan implements it. Domain tables
   (`sources`, `menu_items`, `restaurants`, …) stay in `public`.

2. **Dual-write menu stores.** Today the scrape pipeline writes to **one**
   backend selected by a fragmented set of flags (`--store` on `scrape` is
   inert — `buildMenuStore` hard-codes Weaviate; `--postgres-search` is the
   real selector for serve/index; `--store` only works on `replay-menus`).
   We want local dev to mirror to **both** Postgres and Weaviate (so Weaviate
   can be exercised alongside the prod path), and prod to write **Postgres
   only**. Reads stay Postgres-primary.

3. **TEI embedder.** Replace the in-process Ollama embedder with a dedicated
   [Text Embeddings Inference](https://github.com/huggingface/text-embeddings-inference)
   service serving `nomic-embed-text` (768-dim — matches the existing
   `menu_items.embedding vector(768)` column). Existing menu_items rows keep
   their Ollama-generated vectors (same model, near-identical numerics; no
   re-embed).

The three workstreams share one seam — `pipeline.StoreMenu`
(`pipeline/pipeline.go:257`), which embeds via `search.Embedder.EmbedBatch`
then upserts via `server.MenuStore.BatchUpsertMenu`. Embeddings are fully
decoupled from the store write (the store persists pre-computed
`item.Vector`); this is why the three changes are independently shippable.

## Scope — what changes, what doesn't

| Area | Changes | Stays |
|---|---|---|
| River schema | All 6 `river.NewClient`/migrator sites + the one app SQL read | Domain migrations, `menu_items` table, `restaurants` |
| Menu store | New `DualMenuStore` + unified `NewMenuStore` factory; fix inert `--store` on `scrape` | `MenuStore` interface signature, `pipeline.StoreMenu`, `Searcher` selection, `menu_items` schema |
| Embedder | New `TEIEmbedder` + unified `NewEmbedder` factory; startup dim ping; per-batch dim guard | `Embedder` interface, `pipeline.ToMenuItems` (except the guard), existing `OllamaEmbedder`/`VectorizerClient` (kept as alternatives) |
| Python `scraper` repo | **None** — stateless by design | All of it |
| Postgres schema | None — `menu_items` already has `embedding vector(768)` | All migrations `000000`–`000006` |

## Background — the seam

### Pipeline data flow (today)

```
Go CLI / River worker
  → pipeline.ExtractMenu           (fetch + extract, calls Python scraper)
  → pipeline.StoreMenu              (pipeline/pipeline.go:257)
      → ToMenuItems                 (embeds: Embedder.EmbedBatch, 50/batch)
      → store.BatchUpsertMenu       (persists pre-computed vectors)
  → Store.UpdateScrapeResult        (restaurant status)
```

The store does **not** embed — `pipeline.ToMenuItems` embeds everything first,
attaches `search.MenuItem.Vector`, then hands the slice to
`store.BatchUpsertMenu`. Both `search.PostgresClient.BatchUpsertMenu`
(`search/postgres.go:464`) and `search.Client.BatchUpsertMenu` (Weaviate,
`search/weaviate.go:1070`) write the pre-computed vector unchanged.

### Store selection (today, fragmented)

| Site | File / lines | Key | Builds |
|------|--------------|-----|--------|
| serve | `server/server.go:149-166` | `--postgres-search` (bool) | Postgres / Pinecone / Weaviate → `Searcher` |
| index | `cli/index.go:94-108` | `--postgres-search` | same |
| replay-menus | `cli/restaurants.go:522-541` | `--store=weaviate\|postgres\|pinecone` | Postgres / Weaviate |
| scrape (one-shot) | `cli/scrape.go:249-255` `buildMenuStore` | **inert** — always Weaviate | Weaviate (bug) |

The menutracking pipeline gets its `MenuStore` from a type-assertion on
`srv.Searcher()` (`cli/serve.go:174-177`).

### River client construction (today)

Six sites build a River client or migrator against `public`:

| Site | File / lines | Role |
|------|--------------|------|
| migrator | `cli/db.go:94` | `rivermigrate.New(...).Migrate(Up)` — creates river tables |
| running pipeline | `cli/menutracking_migrate.go:277` | long-running client with workers (`Start`) |
| discover (one-shot) | `cli/restaurants.go:137` | `Insert` only — enqueues discover jobs |
| scrape (one-shot) | `cli/restaurants.go:316` | `Insert` only — enqueues scrape jobs |
| retry | `cli/restaurants.go:373` | `Insert` only |
| replay-menus | `cli/restaurants.go:452` | `Insert` only |

All five `river.NewClient` calls use `&river.Config{}` (no `Schema`), so they
target `public.river_job`. The one app-side SQL read is
`menutracking/store/sql/list_discarded_jobs.sql:3` (`SELECT ... FROM river_job`
unqualified).

### Embedder (today)

`search.Embedder` (`search/embedder.go:8`): `EmbedSingle`, `EmbedBatch`,
`Close`. Two implementations:
- `OllamaEmbedder` (`search/embedder_ollama.go`) — default, calls Ollama
  `/api/embed`, model `nomic-embed-text` (768-dim), prepends
  `search_query:`/`search_document:`.
- `VectorizerClient` (`search/vectorizer.go`) — HTTP proxy fallback.

Injected at construction into both `PostgresClient` and Weaviate `Client`.
`SearchMenu` (query time) calls `embedder.EmbedSingle` for the query vector;
upsert paths use the pre-computed `item.Vector`.

## Workstream A — River tables → `river` schema

**Goal:** River's internal tables live in a dedicated `river` Postgres schema
on fresh setups. Existing deployments are detected and hard-error with a
clear migration message (no silent breakage, no auto-ALTER).

### A.1 Schema config injection

River v0.39 exposes:
- `rivermigrate.Config.Schema string` (`rivermigrate/river_migrate.go:71`)
- `river.Config.Schema string` (`client.go:325`)

The schema must **pre-exist** — River's migrator creates tables but does not
`CREATE SCHEMA`. Validated against
`~/go/pkg/mod/github.com/riverqueue/river@v0.39.0/` source.

**New helpers in `cli/river_client.go`:**

```go
// riverSchema returns the configured River schema (default "river").
func riverSchema() string { return viper.GetString("river-schema") }

// newRiverMigrator builds a River migrator targeting the configured schema.
func newRiverMigrator(pool *pgxpool.Pool) (*rivermigrate.Migrator, error) {
    return rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{
        Schema: riverSchema(),
    })
}

// newRiverClient builds a River client targeting the configured schema.
// Use for all one-shot Insert sites and the long-running pipeline.
func newRiverClient(pool *pgxpool.Pool, cfg *river.Config) (*river.Client[pgx.Tx], error) {
    cfg.Schema = riverSchema()
    return river.NewClient(riverpgxv5.New(pool), cfg)
}
```

**New flag** (in `cli/db.go` `init()` or root command):

```go
rootCmd.PersistentFlags().String("river-schema", "river",
    "Postgres schema for River's internal tables (river_job, river_leader, ...)")
_ = viper.BindPFlag("river-schema", rootCmd.PersistentFlags().Lookup("river-schema"))
_ = viper.BindEnv("river-schema", "RIVER_SCHEMA")
```

### A.2 Patch all 6 sites

| Site | Change |
|------|--------|
| `cli/db.go:94` | `newRiverMigrator(pool)`; add `CREATE SCHEMA IF NOT EXISTS river` before `Migrate` |
| `cli/menutracking_migrate.go:277` | `newRiverClient(pool, &river.Config{...})` |
| `cli/restaurants.go:137,316,373,452` | `newRiverClient(pool, &river.Config{})` |

### A.3 Existing-deployment safety detection

In `cli/db.go` `runDBMigrateUp`, **before** running the River migrator, detect
the half-migrated state:

```go
// Detect existing deployment with river tables in public.
var publicHasRiver, riverHasRiver bool
row := sqldb.QueryRowContext(ctx, `
    SELECT
      EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = 'public' AND tablename = 'river_job'),
      EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = $1 AND tablename = 'river_job')
`, riverSchema())
if err := row.Scan(&publicHasRiver, &riverHasRiver); err != nil {
    return fmt.Errorf("checking river schema state: %w", err)
}
if publicHasRiver && !riverHasRiver {
    return fmt.Errorf(
        "existing deployment detected: river_job exists in public schema but not in %q. "+
        "This build moves River tables to the %q schema. "+
        "Run the one-time migration manually:\n"+
        "  ALTER TABLE SET SCHEMA %s;  -- for each river_* table in public\n"+
        "or drop and re-migrate (loses queued jobs):\n"+
        "  DROP TABLE public.river_job, public.river_leader, public.river_queue, public.river_client CASCADE;\n"+
        "then re-run migrate-up.",
        riverSchema(), riverSchema(), riverSchema())
}
```

This runs **after** domain migrations, **before** `CREATE SCHEMA` + River
migrate. Per chosen option: detect + hard-error.

### A.4 Application SQL read

`menutracking/store/sql/list_discarded_jobs.sql:3` — qualify:

```sql
SELECT kind, args, final_attempt_at, state, created_at FROM river.river_job
```

The file is loaded via `embed.FS` + `text/template` (`menutracking/store` —
verify the exact loader). If it's already templated, add a `{{.Schema}}.`
prefix param; if not, hardcode `river.` (matches the default flag) and
document that a non-default `--river-schema` requires the SQL to be
templated. **Preferred:** make it templated so the flag fully applies.

### A.5 Test updates

- `menutracking/admin_test.go:152` — `CREATE SCHEMA river; CREATE TABLE
  river.river_job (...)` instead of unqualified `public.river_job`. The
  `openTestPool` helper may need `search_path` set to `river,public`.
- New integration test (gated on `POSTGRES_DSN`, as called for in
  `docs/plans/sql-file-management-plan.md:403`): run `db migrate-up` against
  a fresh Postgres, assert `river.river_job` exists and `public.river_job`
  does not; assert the pipeline can enqueue + work a job.

### A.6 Docs

- `docs/guides/troubleshooting.md:227,240,247,270,329,339` — update psql
  examples from `river_job` to `river.river_job`.
- `docs/guides/database-schema.md:27` — already states the `river` schema
  intent; becomes accurate after this change. Add a "Migrating from public"
  note pointing at the detect+hard-error path.

## Workstream B — Dual-write menu stores

**Goal:** Env-driven `--menu-store=postgres|weaviate|dual` (default
`postgres`). `dual` writes Postgres primary + Weaviate best-effort mirror;
reads from Postgres only. Scoped to `MenuStore` — the broader `Searcher`
selection is untouched.

### B.1 `DualMenuStore`

New file `search/dual_store.go`:

```go
// DualMenuStore writes to a primary MenuStore (source of truth) and
// best-effort mirrors to an optional secondary. Reads always go to primary.
// secondary may be nil (then DualMenuStore is a pure passthrough).
type DualMenuStore struct {
    primary   server.MenuStore
    secondary server.MenuStore // nil = primary-only
}

func NewDualMenuStore(primary, secondary server.MenuStore) *DualMenuStore {
    return &DualMenuStore{primary: primary, secondary: secondary}
}

func (d *DualMenuStore) EnsureMenuSchema(ctx context.Context) error {
    if err := d.primary.EnsureMenuSchema(ctx); err != nil {
        return fmt.Errorf("primary ensure menu schema: %w", err)
    }
    if d.secondary != nil {
        if err := d.secondary.EnsureMenuSchema(ctx); err != nil {
            slog.Warn("dual menu store: secondary EnsureMenuSchema failed", "error", err)
        }
    }
    return nil
}

func (d *DualMenuStore) BatchUpsertMenu(ctx context.Context, items []search.MenuItem) error {
    // Primary first — hard error.
    if err := d.primary.BatchUpsertMenu(ctx, items); err != nil {
        return fmt.Errorf("primary upsert: %w", err)
    }
    // Secondary best-effort — log and continue on failure.
    if d.secondary != nil {
        if err := d.secondary.BatchUpsertMenu(ctx, items); err != nil {
            slog.Warn("dual menu store: secondary upsert failed (primary succeeded)",
                "items", len(items), "error", err)
        }
    }
    return nil
}

func (d *DualMenuStore) SearchMenu(ctx context.Context, query string, limit int) ([]search.MenuItem, error) {
    return d.primary.SearchMenu(ctx, query, limit)
}
```

Per chosen option: Weaviate failure is `slog.Warn` + continue, never blocks the
scrape.

### B.2 Unified `NewMenuStore` factory

New file `search/menu_store_factory.go` (or inline in `server/server.go`):

```go
type MenuStoreConfig struct {
    Type          string // "postgres" | "weaviate" | "dual" (default "postgres")
    PostgresDSN   string
    WeaviateHost  string
    WeaviateScheme string
    WeaviateAPIKey string
    Embedder      search.Embedder
}

func NewMenuStore(ctx context.Context, cfg MenuStoreConfig) (server.MenuStore, error) {
    switch cfg.Type {
    case "", "postgres":
        if cfg.PostgresDSN == "" {
            return nil, errors.New("menu-store=postgres requires --postgres-dsn")
        }
        return search.NewPostgresClient(cfg.PostgresDSN, cfg.Embedder)
    case "weaviate":
        if cfg.WeaviateHost == "" {
            return nil, errors.New("menu-store=weaviate requires --weaviate-host")
        }
        wc, err := search.NewClient(cfg.WeaviateHost, cfg.WeaviateScheme, cfg.WeaviateAPIKey, cfg.Embedder)
        if err != nil { return nil, err }
        if err := wc.EnsureMenuSchema(ctx); err != nil { return nil, err }
        return wc, nil
    case "dual":
        if cfg.PostgresDSN == "" || cfg.WeaviateHost == "" {
            return nil, errors.New("menu-store=dual requires both --postgres-dsn and --weaviate-host")
        }
        pg, err := search.NewPostgresClient(cfg.PostgresDSN, cfg.Embedder)
        if err != nil { return nil, err }
        wv, err := search.NewClient(cfg.WeaviateHost, cfg.WeaviateScheme, cfg.WeaviateAPIKey, cfg.Embedder)
        if err != nil { return nil, err }
        if err := wv.EnsureMenuSchema(ctx); err != nil {
            slog.Warn("dual menu store: weaviate EnsureMenuSchema failed", "error", err)
        }
        return NewDualMenuStore(pg, wv), nil
    default:
        return nil, fmt.Errorf("unknown --menu-store %q", cfg.Type)
    }
}
```

**Dual with missing Weaviate config errors explicitly** (E3) — does not
silently degrade.

### B.3 Wire into selection sites

- `server/server.go:149-166` — leave `Searcher` selection alone; add a
  separate `MenuStore` construction using `NewMenuStore`. Plumb the new
  `--menu-store` flag through `server.Config`.
- `cli/index.go:94-108` — replace the three-way switch with `NewMenuStore`.
- `cli/restaurants.go:522-541` (replay-menus) — replace the `--store` switch
  with `NewMenuStore` (keep `--store` as a deprecated alias mapping to
  `--menu-store`, or remove and document).
- **`cli/scrape.go:249-255` `buildMenuStore`** — replace the hard-coded
  Weaviate construction with `NewMenuStore`. This **fixes a pre-existing
  bug** (the `--store` flag on `scrape` was inert). Call this out in the
  commit message.
- `cli/serve.go:174-177` — the type-assertion stays; `DualMenuStore`
  satisfies `server.MenuStore`.

### B.4 No schema changes

`menu_items` (Postgres) and `RestaurantMenu` (Weaviate collection) both
already exist and carry the 768-dim vector. No migration needed.

## Workstream C — TEI embedder

**Goal:** Add a TEI-backed `Embedder` so embeddings come from a dedicated TEI
service serving `nomic-embed-text` (768-dim, matches existing vectors).

### C.1 `TEIEmbedder`

New file `search/embedder_tei.go`:

```go
// TEIEmbedder implements Embedder against a HuggingFace Text Embeddings
// Inference (TEI) service. It serves nomic-embed-text (768-dim) by default.
type TEIEmbedder struct {
    baseURL string
    model   string
    prefix  string // "" or "nomic" — nomic-embed-text wants "search_query:"/"search_document:" prefixes
    client  *http.Client
}

func NewTEIEmbedder(baseURL, model string) *TEIEmbedder {
    return &TEIEmbedder{
        baseURL: strings.TrimRight(baseURL, "/"),
        model:   model,
        client:  &http.Client{Timeout: 30 * time.Second},
    }
}

// EmbedSingle prepends the query-time prefix (nomic: "search_query: ").
func (e *TEIEmbedder) EmbedSingle(ctx context.Context, text string) ([]float32, error) {
    return e.embed(ctx, []string{"search_query: " + text}, true)
}

// EmbedBatch prepends the document-time prefix per text. Returns vectors
// in input order — required by pipeline.ToMenuItems.
func (e *TEIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
    if len(texts) == 0 { return nil, nil }
    in := make([]string, len(texts))
    for i, t := range texts { in[i] = "search_document: " + t }
    return e.embed(ctx, in, false)
}

func (e *TEIEmbedder) Close() error { return nil }
```

**Response shape:** TEI's `/embed` returns `{"embeddings": [[...]]}` (older)
or bare `[[...]]` (newer "load" endpoint). Handle both via content-type
sniffing or a try-both decode (R7). Pin to the known TEI version in
production; the embedder must be robust to both during local dev.

### C.2 Unified `NewEmbedder` factory

New file `search/embedder_factory.go`:

```go
type EmbedderConfig struct {
    Type         string // "ollama" | "tei" | "vectorizer" (default "ollama")
    OllamaURL    string
    OllamaModel  string
    TEIURL       string
    TEIModel     string
    VectorizerURL string
}

func NewEmbedder(ctx context.Context, cfg EmbedderConfig) (search.Embedder, error) {
    var e search.Embedder
    switch cfg.Type {
    case "", "ollama":
        e = search.NewOllamaEmbedder(cfg.OllamaURL, cfg.OllamaModel)
    case "tei":
        if cfg.TEIURL == "" { return nil, errors.New("embedder=tei requires --tei-url") }
        e = search.NewTEIEmbedder(cfg.TEIURL, cfg.TEIModel)
    case "vectorizer":
        if cfg.VectorizerURL == "" { return nil, errors.New("embedder=vectorizer requires --vectorizer-url") }
        e = search.NewVectorizerClient(cfg.VectorizerURL)
    default:
        return nil, fmt.Errorf("unknown --embedder %q", cfg.Type)
    }
    // Startup ping + dimension validation (R3, E2). Runs before EnsureMenuSchema.
    vec, err := e.EmbedSingle(ctx, "ping")
    if err != nil {
        return nil, fmt.Errorf("embedder startup ping failed: %w", err)
    }
    const expectedDim = 768
    if len(vec) != expectedDim {
        return nil, fmt.Errorf("embedder returned %d-dim vectors, expected %d (menu_items.embedding is vector(768))", len(vec), expectedDim)
    }
    return e, nil
}
```

**Default stays `ollama`** — TEI is opt-in. Existing deployments are
unaffected unless they explicitly set `--embedder=tei`.

**`expectedDim`** is a constant — if the model changes (different dim), the
`menu_items.embedding vector(768)` column needs a migration and all rows
need re-embedding. Flag this in the TEI section of `docs/guides/` (E1).

### C.3 Wire into construction sites

- `cli/serve.go` (the embedder construction inside `runServe`)
- `cli/index.go`
- `cli/scrape.go`

All currently call `search.NewOllamaEmbedder(...)` directly. Replace with
`search.NewEmbedder(ctx, cfg)`.

### C.4 Per-batch dim guard (defense-in-depth)

In `pipeline/pipeline.go:300-310` (`ToMenuItems`), after each `EmbedBatch`:

```go
batchVectors, err := embedder.EmbedBatch(ctx, texts[i:end])
if err != nil { return nil, fmt.Errorf("embedding batch [%d:%d]: %w", i, end, err) }
for j, v := range batchVectors {
    if len(v) != 768 {
        return nil, fmt.Errorf("embedding batch [%d:%d]: vector %d has dim %d, expected 768", i, end, i+j, len(v))
    }
}
vectors = append(vectors, batchVectors...)
```

This catches a misconfigured TEI silently truncating/expanding even if the
startup ping passed (e.g. model swap mid-run).

### C.5 Contract doc

Add a comment to `search/embedder.go:8`:

```go
// Embedder generates vector embeddings from text.
// Implementations MUST preserve input order in EmbedBatch: the i-th returned
// vector must be the embedding of the i-th input text. pipeline.ToMenuItems
// relies on this correspondence to attach vectors to menu items.
type Embedder interface { ... }
```

This codifies the contract that `OllamaEmbedder` already satisfies and that
`TEIEmbedder` must satisfy (R8).

## Implementation order

**C → B → A.**

- **C first** (TEI embedder): self-contained, no schema/store changes.
  Unblocks B's local-dev testing (B's dual-write needs a working embedder to
  produce vectors for both stores).
- **B** (dual-write): depends on C for a real embedder in local dev.
- **A** (river schema): fully independent; done last so a River schema mishap
  can't block the other two. Also the highest-risk change (touches 6 sites +
  safety detection), so best done when C and B are stable.

## Tests (new)

| File | Covers |
|---|---|
| `search/embedder_tei_test.go` | httptest TEI server; prefix prepending; order preservation (R8); both response shapes (R7); empty batch; dim validation |
| `search/embedder_factory_test.go` | factory selection; startup ping failure; dim mismatch rejection |
| `search/dual_store_test.go` | primary ok + secondary ok → no error; primary ok + secondary fail → no error + warn logged; primary fail → error; secondary nil → passthrough |
| `search/menu_store_factory_test.go` | `--menu-store=postgres\|weaviate\|dual`; dual with missing config errors (E3); default = postgres |
| `menutracking/admin_test.go` (update) | `river.river_job` stub in `river` schema |
| `cli/db_migrate_test.go` (new, `POSTGRES_DSN`-gated) | fresh migrate-up → `river.river_job` exists, `public.river_job` does not; existing-deploy detection fires when `public.river_job` present + `river.river_job` absent |
| `pipeline/pipeline_test.go` (extend) | per-batch dim guard rejects wrong-dim vectors |
| `cli/scrape_test.go` (extend) | `buildMenuStore` honors `--menu-store` (regression test for the dead-flag fix, R6) |

## Risks, gaps, tradeoffs, edge cases

### Risks

- **R1 — River schema: must patch all 6 client sites, not 2.** Mitigated by
  the shared `newRiverClient`/`newRiverMigrator` helpers (A.1, A.2). A single
  missed site silently orphans jobs (one-shot `restaurants` commands enqueue
  into `public.river_job`; the running pipeline reads `river.river_job`).
- **R2 — River's migrator does not `CREATE SCHEMA`.** Mitigated by the
  explicit `CREATE SCHEMA IF NOT EXISTS river` before `Migrate` (A.2).
- **R3 — Test stub breaks.** `menutracking/admin_test.go:152` creates an
  unqualified `river_job` stub. Mitigated by A.5 (create stub in `river`
  schema, set `search_path`).
- **R4 — Qualified vs. `search_path` for River reads.** Chose qualified
  `river.river_job` in the one app SQL read (A.4). Manual psql diagnostic
  queries in `troubleshooting.md` are updated (A.6). River's own internal
  queries are schema-qualified via its `Config.Schema` — no `search_path`
  coupling.
- **R5 — `MenuStore` dual-write must not touch `Searcher` selection.** The
  broader `Searcher` (reviews/fodmap/businesses) selection at
  `server/server.go:149-166` is left alone. A user running
  `--menu-store=dual` with `--postgres-search=false` gets Postgres-primary
  menu writes + Weaviate menu mirror + Weaviate reviews/fodmap reads.
  Documented (inconsistent but explicit; menus and reviews are different
  surfaces).
- **R6 — Pre-existing `--store` dead-flag bug on `scrape`.** Fixing
  `buildMenuStore` to honor `--menu-store` is a side-effect bugfix. Called out
  in the commit message. Regression test in `cli/scrape_test.go`.
- **R7 — TEI response shape ambiguity.** Mitigated by content-type sniffing
  / try-both decode (C.1) and the httptest test (C tests).
- **R8 — `EmbedBatch` ordering guarantee.** Mitigated by the contract doc
  (C.5) + the order-preservation test in `embedder_tei_test.go`.
- **R9 — Empty batch / empty text behavior.** `EmbedBatch([])` returns
  `nil, nil` (C.1). `pipeline.StoreMenu` short-circuits on empty items
  (already the case). `ToMenuItems` is never called with zero items because
  of that short-circuit, but the per-batch dim guard is defensive.

### Gaps

- **G1 — Docs out of date.** `docs/guides/database-schema.md:27` already
  states the `river` schema intent (aspirational before this change, accurate
  after). `docs/guides/troubleshooting.md` has unqualified `river_job` psql
  examples that need `river.` qualification. Both addressed in A.6.
- **G2 — Long-standing intent.** `docs/plans/regtrack-pipeline-plan.md:9`
  already specifies "we isolate in a dedicated `river` Postgres schema" —
  this change implements documented intent, not new architecture. Good
  signal.
- **G3 — No SQL migration file, but a transition path is required.**
  Per chosen option (detect + hard-error), `runDBMigrateUp` checks for the
  half-migrated state and returns a clear migration message (A.3). No
  auto-ALTER, no silent breakage. Existing deployments must run the one-time
  `ALTER TABLE SET SCHEMA` manually or drop and re-migrate.
- **G4 — `pipeline.StoreMenu` signature unchanged.** No caller updates
  needed beyond the store instance passed in.
- **G5 — `SearchMenu` filter.** Postgres and Weaviate `SearchMenu` both
  take `(query, limit)` — no `SearchFilter` for menus (unlike reviews).
  Dual-store reads from Postgres primary only. No gap, noted for clarity.
- **G6 — Integration test for river schema.** Already called for in
  `docs/plans/sql-file-management-plan.md:403`. Added in A.5.

### Tradeoffs

- **T1 — Qualified reads over `search_path`.** Sacrifices a tiny bit of
  manual-query ergonomics for explicitness; no `search_path` coupling.
- **T2 — Best-effort mirror over transactional dual-write.** Weaviate can
  silently drift behind Postgres. Accepted per decision. Direct Weaviate
  reads (not via `SearchMenu`) may see stale/missing data — documented.
- **T3 — Default `--menu-store=postgres`.** Prod-safe default; local dev
  must explicitly set `--menu-store=dual`. Rejected the inverse (default
  dual, fail if Weaviate missing) for prod safety.
- **T4 — Default `--embedder=ollama`.** TEI is opt-in. Avoids surprising
  existing deployments. Existing vectors stay valid (same model).

### Edge cases

- **E1 — Existing `menu_items` rows.** Same model (nomic-embed-text) via
  TEI produces near-identical vectors. Per chosen option: no re-embed.
  Documented in the TEI guide section.
- **E2 — TEI startup ping before schema.** Ping runs in `NewEmbedder`
  before `EnsureMenuSchema` (C.2). A failed ping fails fast before any
  schema/collection is touched.
- **E3 — Dual-store with missing Weaviate config.** `NewMenuStore` errors
  explicitly (B.2), does not silently degrade to Postgres-only.
- **E4 — River schema name collision.** `CREATE SCHEMA IF NOT EXISTS river`
  is idempotent. If a deployment already uses `river` for something else,
  River's tables coexist (named `river_job` etc.). Unlikely; the flag is
  configurable via `--river-schema` if needed.
- **E5 — `DiscardedJobRetentionPeriod` 30 days.** Unchanged. Not in scope.

## Verification

### Per workstream

**C (TEI embedder):**
- `go test ./search/...` — new `embedder_tei_test.go` + `embedder_factory_test.go`.
- Run `go run . scrape <url> --embedder=tei --tei-url <tei> --menu-store=postgres`
  against a real TEI URL; confirm vectors land in `menu_items.embedding`
  with `len == 768` (query: `SELECT menu_item_id, vector_dims(embedding) FROM menu_items ORDER BY scraped_at_utc DESC LIMIT 5;`).

**B (dual-write):**
- `go test ./search/... ./pipeline/... ./cli/... ./server/...` — new
  `dual_store_test.go` + `menu_store_factory_test.go`; extend `stubMenuStore`
  to a `dualStoreStub` covering primary-ok/secondary-fail.
- Run dual mode locally: `go run . scrape <url> --menu-store=dual
  --postgres-dsn ... --weaviate-host ... --embedder=ollama`. Confirm both
  stores populate (psql + Weaviate GraphQL).
- Flip to `--menu-store=postgres`; confirm Weaviate is untouched on re-scrape
  (idempotent upsert via deterministic `menu_item_id`).

**A (river schema):**
- `go test ./menutracking/... ./cli/...` — updated `admin_test.go` + new
  `db_migrate_test.go`.
- Against a **fresh** Postgres: `go run . db migrate-up`; assert
  `river.river_job` exists and `public.river_job` does not. Start the
  pipeline; confirm enqueue + work + dequeue.
- Against an **existing** deployment (`public.river_job` present,
  `river.river_job` absent): confirm `migrate-up` hard-errors with the
  migration message.
- `list_discarded_jobs` reads `river.river_job` (or whatever
  `--river-schema` is set to).

### Whole-system

After all three: run the menutracking pipeline end-to-end with
`--menu-store=dual --embedder=tei --river-schema=river`. Confirm:
- jobs enqueue/work from `river.river_job`,
- menu items embed via TEI (768-dim) and land in both Postgres + Weaviate,
- a Weaviate failure (kill the Weaviate container mid-scrape) logs a warn
  and does not fail the scrape,
- `SearchMenu` reads return Postgres results.

## Rollback

Each workstream is independently revertible:
- **C:** unset `--embedder=tei` (returns to `ollama`). Existing menu_items
  vectors stay (same model).
- **B:** unset `--menu-store=dual` (returns to `postgres`-only). No schema
  changes to undo.
- **A:** revert the 6 site patches + the `list_discarded_jobs.sql`
  qualification. Existing `river.river_job` stays in `river`; to move back
  to `public`, run `ALTER TABLE river.river_job SET SCHEMA public;` (and the
  same for `river_leader`, `river_queue`, `river_client`,
  `river_migration`).

## See also

- `docs/plans/regtrack-pipeline-plan.md:9` — original River schema intent.
- `docs/plans/sql-file-management-plan.md:403` — calls for the
  `POSTGRES_DSN`-gated river-schema integration test.
- `docs/guides/database-schema.md:27` — schema doc (becomes accurate).
- `docs/guides/troubleshooting.md` — psql diagnostic examples (updated).
- `../scraper/AGENTS.md` — Python repo's stateless-by-design rule (why
  none of this touches Python).