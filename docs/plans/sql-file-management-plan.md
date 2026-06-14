# Plan: Tidy SQL File Management

**Status:** Implemented (Option C — embedded migrations with `golang-migrate`)

> **Implementation note:** This plan has been fully executed. DDL now lives in `internal/db/migrations/`, applied by `golang-migrate` via `go run . db migrate-up`. The old inline DDL in `auth/postgres_store.go` and `search/postgres.go` has been removed; `EnsureSchema`/`EnsureFodmapSchema` on PostgresClient are now no-ops; `menutracking/store/schema.sql` and `fodmap/store/sql/schema.sql` have been deleted; `menutracking MigrateUp` is a no-op. See `docs/guides/database-schema.md` for the current architecture. The historical analysis below is preserved for reference.

## Problem Statement

SQL files are accumulating in several package subdirectories and are starting to feel unwieldy for the following reasons:

1. **DDL and DML are mixed in package subdirectories, and some DDL is inlined in Go.** *(Resolved — DDL moved to `internal/db/migrations/`)*
   - `fodmap/store/sql/` contained both schema/seed DDL (`schema.sql`, `seed.sql`) and runtime CRUD queries. `schema.sql` has been deleted.
   - `menutracking/store/schema.sql` was embedded and run as a one-shot setup script. Deleted.
   - **`auth` and `search` embedded their table DDL directly in Go source as inline `CREATE TABLE IF NOT EXISTS` strings.** This inline DDL has been removed. `EnsureSchema`/`EnsureFodmapSchema` on PostgresClient are now no-ops.
2. **Many tiny query files.**
   - `auth/sql/` has 12 separate one-statement files.
   - `fodmap/store/sql/` has 12 files (including schema and seed).
   - `menutracking/store/sql/` has 10 files.
3. **No consistent migration strategy.** *(Resolved — all DDL now managed by `golang-migrate` via `internal/db/`)*
   - `menutracking` used a bespoke CLI command (`go run . menutracking migrate-up`) that ran `river migrate-up` plus its own embedded `schema.sql`. Now a no-op; replaced by `go run . db migrate-up`.
   - `fodmap` used a `fodmap_meta` key to ensure seed data is applied only once. Seed logic stays in Go code.
   - `auth` and `search` created their tables as a side effect of constructor/`EnsureSchema` calls at startup, relying on `CREATE TABLE IF NOT EXISTS` idempotency rather than tracked migrations. Now no-ops; tables created by `golang-migrate`.
4. **No single place to see the whole database shape.** *(Resolved — `internal/db/migrations/` + `docs/guides/database-schema.md`)*
   - A developer must open `auth/postgres_store.go`, `fodmap/store/sql/schema.sql`, `menutracking/store/schema.sql`, `search/postgres.go`, etc. to understand the full Postgres schema — and two of those are Go files, not SQL.

## Current File Inventory

```
auth/sql/                          # 12 runtime query files (plain SQL, one statement each)
auth/postgres_store.go             # table DDL INLINE in Go (users, user_profiles, conversations, messages)
fodmap/store/sql/                  # 12 files: DDL + seed + template queries
menutracking/store/schema.sql      # DDL (embedded one-shot, 58 lines, uses IF NOT EXISTS)
menutracking/store/sql/            # 10 runtime query files (plain SQL, one statement each)
search/sql/                        # 2 runtime query files (Go templates with {{.Where}}, {{.LimitArg}})
search/postgres.go                 # table DDL INLINE in Go (reviews, review_chunks, fodmap_ingredients)
```

### Full Table Inventory

The complete Postgres schema (excluding River's own `river_*` tables) is spread across four locations, two of which are Go files:

| Table | Defined in | Form |
|---|---|---|
| `users`, `user_profiles`, `conversations`, `messages` | `auth/postgres_store.go:71-99` | inline Go `CREATE TABLE IF NOT EXISTS` |
| `reviews`, `review_chunks`, `fodmap_ingredients` | `search/postgres.go:66` (`EnsureSchema`) | inline Go `CREATE TABLE IF NOT EXISTS` |
| `fodmap_catalog`, `fodmap_meta` | `fodmap/store/sql/schema.sql` | embedded `.sql` |
| menutracking tables | `menutracking/store/schema.sql` | embedded `.sql`, `IF NOT EXISTS` |

**Note the two distinct fodmap tables:** `fodmap_ingredients` is owned by `search`, while `fodmap_catalog`/`fodmap_meta` are owned by `fodmap/store`. Both must be ported — do not conflate them.

### Current Embedding Patterns

The codebase uses three different patterns for embedding SQL, which affects how consolidation would work:

| Package | Embed style | Template engine | Example |
|---|---|---|---|
| `auth` | One `//go:embed` var per query file | None (plain `$1`, `$2`) | `var listUsersSQL string` |
| `menutracking/store` | One `//go:embed` var per query file | None (plain `$1`, `$2`) | `var ListSourcesSQL string` |
| `fodmap/store` | `embed.FS` glob (`sql/*.sql`) | `text/template` (dynamic WHERE, LIMIT, OFFSET) | `sqlTemplates.ExecuteTemplate(...)` |
| `search` | `embed.FS` directory (`sql`) | `text/template` (dynamic WHERE, LIMIT) | `sqlTemplates.ExecuteTemplate(...)` |

**Key implication:** `fodmap/store` and `search` render SQL at runtime via Go templates. Any consolidation approach must preserve this — simple `-- name:` delimiter splitting would not work for these packages because the template placeholders (`{{.Where}}`, `{{.LimitArg}}`, `{{.OffsetArg}}`) are not valid SQL and must be rendered by `text/template` before execution. Only `auth` and `menutracking/store` could use a `-- name:` splitting approach without template support.

## Design Constraints

- CLAUDE.md mandates SQL in `.sql` files under `sql/` (or equivalent), embedded via `//go:embed`, and parameterized with `$1`, `$2`, etc.
- No inline SQL in Go code.
- Keep the project simple: avoid heavy DevOps/SRE tooling unless the team already operates it.
- Existing `make check` and `start.sh` behavior should keep working during any transition.
- `search/sql/` and `fodmap/store/sql/` query files use Go templates and must continue to work with `text/template` rendering.

## Proposed Options

### Option A: Consolidate Query Files Per Package (No Migrations)

**What it is**

Keep SQL embedded in package `sql/` directories, but group statements into fewer files per package. DDL stays in `schema.sql`; runtime queries go into one or two consolidated files:

```
auth/sql/
  queries.sql        # all SELECT/INSERT/UPDATE/DELETE
  schema.sql         # DDL (still embedded, still one-shot)

fodmap/store/sql/
  queries.sql        # template queries (preserves {{.Where}}, etc.)
  schema.sql
  seed.sql

menutracking/store/sql/
  queries.sql
  schema.sql         # DDL (moved from parent directory)

search/sql/
  queries.sql        # template queries (preserves {{.Where}}, etc.)
```

Each Go file embeds the relevant named file with `//go:embed sql/queries.sql` etc.

The application can still pick an individual statement out of a consolidated file by splitting on a chosen delimiter. For example, a single `sql/queries.sql` can contain commented markers:

```sql
-- name: create
INSERT INTO ingredients (name, group_name, level) VALUES ($1, $2, $3) RETURNING name;

-- name: list
SELECT name, group_name, level FROM ingredients ORDER BY name LIMIT $1 OFFSET $2;
```

And the Go code can parse it at init time:

```go
//go:embed sql/queries.sql
var queriesSQL string

var ingredientQueries = map[string]string{}

func init() {
    const marker = "-- name: "
    for _, part := range strings.Split(queriesSQL, marker) {
        if part == "" {
            continue
        }
        lines := strings.SplitN(part, "\n", 2)
        ingredientQueries[lines[0]] = strings.TrimSpace(lines[1])
    }
}
```

Alternatively, keep fewer files grouped by purpose (`sql/queries/create.sql`, `sql/queries/list.sql`, etc.) and embed each one explicitly. Either way, the application retains full control over which statement it uses.

**Important caveat for template-based packages:** The `-- name:` splitting approach only works for plain-SQL packages (`auth`, `menutracking`). For `fodmap/store` and `search`, which use `text/template` with `embed.FS` and `template.ParseFS`, consolidation must preserve the template filenames as template names. A single `queries.sql` file with `-- name:` markers cannot be used with `template.ParseFS` because the template engine needs separate files to identify templates by name. These packages would need to either:
- Keep separate `.sql` template files in a subdirectory (e.g. `sql/templates/list.sql`), or
- Use a single file with `{{define "list"}}...{{end}}` template definitions.

**Pros**

- Very small change. Mainly file moves/merges.
- Still follows the `//go:embed sql/*.sql` rule.
- Reduces file-count noise immediately.
- No new dependencies.
- Individual queries remain selectable by the application.

**Cons**

- DDL is still scattered across packages; no global schema view.
- Still no real migrations; schema changes remain risky in production.
- Seed-once logic (`fodmap_meta`, custom checks) still has to be maintained by hand.
- Does not solve the long-term maintainability problem, only the file-count symptom.
- Parsing by delimiter is hand-rolled logic that could drift if a query happens to contain the delimiter string.
- Does not simplify the template-based packages (`fodmap/store`, `search`) — they would still need separate files or `{{define}}` blocks.

**Best for**

Teams that want a quick cleanup now and plan to revisit migrations later.

---

### Option B: Centralized Timestamped Migrations + Per-Package Queries

**What it is**

Move all DDL out of package `sql/` directories and into a single top-level `migrations/` directory with versioned files:

```
migrations/
  000001_create_users_conversations_auth.up.sql
  000002_create_search_tables.up.sql
  000003_create_menutracking_tables.up.sql
  000004_create_fodmap_catalog.up.sql
```

Use `golang-migrate/migrate` or `pressly/goose` with the Postgres driver. Each package keeps only its runtime queries under its own `sql/` directory.

Add a CLI command such as `go run . db migrate-up` that runs migrations on startup or on demand, and update `start.sh` to call it.

**Note on migration tool choice:**

- **`golang-migrate/migrate`**: Widely used, supports `iofs` source for embedded migrations, has a CLI companion, and works well with `*sql.DB`. Pure Go (no C dependencies). Import path for Postgres driver: `github.com/golang-migrate/migrate/v4/database/postgres`.
- **`pressly/goose`**: Also widely used, supports Go and SQL migrations, simpler API. Can run migrations from `embed.FS`. Import path: `github.com/pressly/goose/v3`.

Both are production-viable. The choice between them is largely stylistic.

**Pros**

- Single source of truth for schema history.
- Migrations are versioned, ordered, and reviewable.
- Reversible (with `.down.sql` files when using a tool that supports them).
- Runtime query files stay close to the code that uses them.
- Decouples DDL lifecycle from application startup.

**Cons**

- New dependency (`golang-migrate/migrate` or `pressly/goose`).
- Requires changing `menutracking migrate-up` and `fodmap` seed logic.
- Must keep migration files in sync with package query assumptions.
- Slightly more setup for local development and CI.
- Migrations read from disk (unless combined with Option C's embedding).

**Best for**

Teams ready to adopt a real migration workflow and willing to take a small dependency.

---

### Option C: Embedded Migrations with `golang-migrate`

**What it is**

Same structure as Option B, but the migration files are embedded into the binary with `//go:embed` so the application is self-contained:

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS

func Migrate(db *sql.DB) error {
    driver, err := postgres.WithInstance(db, &postgres.Config{})
    if err != nil {
        return fmt.Errorf("create migrate driver: %w", err)
    }
    src, err := iofs.New(migrationsFS, "migrations")
    if err != nil {
        return fmt.Errorf("create migration source: %w", err)
    }
    m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
    if err != nil {
        return fmt.Errorf("create migrate instance: %w", err)
    }
    if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
        return fmt.Errorf("run migrations: %w", err)
    }
    return nil
}
```

A single `migrations/` directory lives at the project root. The CLI exposes `go run . db migrate-up` and `migrate-down`/`migrate-force` if desired.

**Note:** The import path for the Postgres driver is `github.com/golang-migrate/migrate/v4/database/postgres` (not `pgx/v5`), because the codebase uses `database/sql` with the `pgx/stdlib` driver. `postgres.WithInstance` accepts `*sql.DB` directly.

**Relationship to Option B:** Options B and C are not fundamentally different — both use a migration tool with timestamped SQL files. The only distinction is whether migrations are read from disk (B) or embedded in the binary (C). You could start with B and add embedding later, or start with C directly.

**Pros**

- Self-contained binary; no file-system path configuration for migrations.
- Consistent with existing `//go:embed` preference for static assets and SQL.
- Versioned, ordered schema history.
- Reusable across server, CLI, and tests.

**Cons**

- Still adds the `golang-migrate` dependency.
- Slightly more plumbing in the `db` package or `server` startup.
- Tests that use `sqlmock` may need adjustment if they currently rely on `schema.sql` being executed by the store constructor.

**Best for**

This project. It satisfies the CLAUDE.md embedding rule, solves the migration problem, and keeps the deployment simple.

---

### Option D: Schema-as-Code Tool (Atlas, dbmate, tern)

**What it is**

Replace hand-written migration files with a higher-level tool:

- **Atlas:** define a desired schema in HCL/DDL and let Atlas generate migration diffs.
- **dbmate:** plain SQL up/down files with lightweight CLI, environment-driven.
- **tern:** Postgres-specific, supports Go migrations and embeddable files.

These tools usually run outside the Go binary (except `tern` which can be embedded).

**Pros**

- Less manual migration writing for some tools (especially Atlas with HCL).
- Good dev/prod schema drift detection.
- Some tools have CI-friendly linting/diffing.

**Cons**

- Additional external tool to install and learn.
- Atlas HCL is not plain SQL; diverges from the CLAUDE.md preference of putting SQL in `.sql` files.
- Heavier than the project likely needs.
- `start.sh` and CI must install the tool.

**Best for**

Teams already using these tools operationally, or projects with multiple developers managing a complex evolving schema.

---

## What Changes If We Allow Inline SQL in Go

The CLAUDE.md rule "Never inline SQL queries in Go code" is the main reason `auth/sql/` has 12 one-statement files and `menutracking/store/sql/` has 11. Dropping this rule would let plain SQL queries live as Go string constants, which changes the tradeoffs significantly.

### Which queries could move inline

| Package | Query style | Could go inline? | Why |
|---|---|---|---|
| `auth` | Plain `$1`/`$2` parameters, one statement per file | **Yes** — all 12 queries are short (1–9 lines each) and have no template logic |
| `menutracking/store` | Plain `$1`/`$2` parameters, one statement per file | **Yes** — all 11 queries are short (4–12 lines each) |
| `fodmap/store` | Go `text/template` with `{{.Where}}`, `{{.LimitArg}}`, `{{.OffsetArg}}` | **Partially** — simple queries like `create`, `get`, `delete` could go inline, but dynamic queries (`list`, `stats`) need templates and are more readable in `.sql` files |
| `search` | Go `text/template` with `{{.Where}}`, `{{.LimitArg}}` | **No** — queries are 56+ lines with complex CTEs; templates are essential for dynamic WHERE/LIMIT clauses |

### Impact on each option

**Option A (consolidate only):** If inline SQL is allowed, `auth` and `menutracking/store` no longer need separate `.sql` files at all. Their queries would become Go constants in `postgres_store.go` and `migrate.go` respectively. This eliminates 23 `.sql` files immediately. `fodmap/store` and `search` would still keep their template `.sql` files, but `fodmap/store` could inline its simple queries (`create`, `get`, `delete`, `update`, `count`, `get_meta`, `set_meta`) and keep only the dynamic template queries (`list`, `list_all`, `stats`) in `.sql` files. Total file count drops from ~37 to ~8–10.

**Option B/C (migrations):** Inline SQL does not change the migration story at all — DDL still needs versioned migration files regardless of where query strings live. The only effect is that fewer `.sql` query files remain in each package directory after DDL is moved out.

**Option D (schema-as-code):** Same as B/C — no material change to the tooling choice.

### Revised file counts if inline SQL is allowed

| Package | Current files | Option A | Option B/C |
|---|---|---|---|
| `auth/sql/` | 12 | 0 (all inline) | 0 (all inline) |
| `menutracking/store/sql/` | 11 | 0 (all inline) | 0 (all inline) |
| `menutracking/store/schema.sql` | 1 | 1 (still one-shot) | 0 (moved to migrations/) |
| `fodmap/store/sql/` | 12 | 3–5 (template queries only) | 3–5 (template queries only) |
| `fodmap/store/sql/schema.sql` + `seed.sql` | 2 | 2 (still one-shot) | 0 (moved to migrations/) |
| `search/sql/` | 2 | 2 (templates must stay) | 2 (templates must stay) |
| `migrations/` (new) | 0 | 0 | 4–5 |
| **Total** | ~40 | 6–8 | 9–12 |

### Tradeoff

Allowing inline SQL eliminates the file-count problem for `auth` and `menutracking` entirely, and reduces `fodmap/store` to just its template queries. The cost is:

- SQL lives in Go files alongside Go code, making it harder to audit queries without reading Go source.
- No single place to see all queries for a package — they are scattered across struct methods.
- IDE SQL tooling (syntax highlighting, formatting, explain) works less well on Go string constants than on `.sql` files.
- The rule exists to enforce separation of concerns; removing it trades organizational discipline for convenience.

If the team decides to drop the rule, the simplest path is:

1. Inline all plain SQL for `auth` and `menutracking/store`.
2. Inline simple queries in `fodmap/store`, keep template queries as `.sql` files.
3. Still adopt Option C for DDL/migrations — inline queries do not solve the migration problem.

This combination (inline queries + Option C migrations) gives the lowest file count while still gaining real migrations.

---

## Comparison Table

| Criterion | Option A (consolidate only) | Option B (central migrations) | Option C (embedded migrations) | Option D (schema-as-code) | Inline SQL + Option C |
|---|---|---|---|---|---|
| File-count noise | Reduced | Eliminated for DDL | Eliminated for DDL | Eliminated for DDL | Near-zero (9–12 total) |
| Single schema view | No | Yes | Yes | Yes | Yes |
| Real migrations | No | Yes | Yes | Yes | Yes |
| Self-contained binary | Yes | No (files on disk) | Yes | Varies | Yes |
| New Go dependency | None | Small (`golang-migrate` or `goose`) | Small (`golang-migrate` or `goose`) | Small-to-medium | Small (`golang-migrate`) |
| External tooling | None | None | None | Atlas CLI / dbmate / tern CLI | None |
| Aligns with `//go:embed` rule | Yes | Partial | Yes | Partial / Varies | Yes (for migrations + templates) |
| Handles template queries | Must keep separate files or use `{{define}}` | Same | Same | Same | Same — templates stay as `.sql` |
| SQL separate from Go | Yes | Yes | Yes | Yes | No — plain SQL moves into Go constants |
| Production safety | Low | Medium | Medium | Medium-High | Medium |

Note: Options B and C can be combined — use `golang-migrate` with `iofs` embedding for production, but read from disk during development for faster iteration.

## Recommended Approach

**Option C: embedded migrations with `golang-migrate`.**

The strongest justification is not file-count cleanup — it is that **the database currently has no single, readable definition of its own schema, in code or in docs.** The DDL is scattered across four locations, two of which are Go source files (`auth/postgres_store.go`, `search/postgres.go`), and there is no prose documentation of the tables anywhere (`data-model.md` covers only Go structs; troubleshooting covers only the Weaviate vector schema). A new contributor cannot answer "what does the `conversations` table look like?" without grepping Go constructors. Consolidating DDL into a top-level `migrations/` directory makes that schema legible in one place for the first time, and the new `database-schema.md` (transition step 10) gives it a documented entry point.

Beyond that, Option C also solves the file sprawl and the lack of real migrations in one move, stays consistent with the `//go:embed` convention already used throughout the codebase, and keeps the binary self-contained. River's own migrations should remain separate (handled by `river migrate-up`) to avoid version-skew issues.

## Suggested Transition Plan

> All steps below have been implemented.

1. **Add dependency:** `go get github.com/golang-migrate/migrate/v4 github.com/golang-migrate/migrate/v4/database/postgres github.com/golang-migrate/migrate/v4/source/iofs`. ✅
2. **Create `migrations/`** at project root and port current DDL from all four sources. ✅ — Migrations live under `internal/db/migrations/` (not project root, because `//go:embed` can't reach outside its package directory).
3. **Establish a baseline migration for existing databases.** ✅ — Baseline uses `IF NOT EXISTS` on all statements so it's safe against both fresh and existing databases. Existing databases can also use `db migrate-force 1`.
4. **Create a `db` package** (e.g. `internal/db/migrate.go`) that embeds `migrations/` and exposes `Migrate(db *sql.DB) error`. ✅
5. **Update CLI:** add `db migrate-up` (and optionally `migrate-down`, `migrate-version`, `migrate-force`). ✅
6. **Update `start.sh`** to run `db migrate-up` before starting the server. ✅
7. **Remove the old schema-creation code paths so the schema has a single owner:** ✅
   - Removed `schema.sql` from `fodmap/store/sql/` and `menutracking/store/`.
   - Kept `seed.sql` as application-level seed logic (not a migration).
   - Deleted the inline DDL from `auth.NewPostgresStore` and made `search.(*PostgresClient).EnsureSchema`/`EnsureFodmapSchema` no-ops.
8. **Update tests:** ✅ — `search/postgres_test.go` EnsureSchema/EnsureFodmapSchema tests now verify no-op behavior.
9. **Update the SQL rules in both CLAUDE.md _and_ `.rules/sql.md`.** ✅
10. **Document the database schema.** ✅ — `docs/guides/database-schema.md` added and cross-linked.
11. **Run `make check`** and update remaining documentation. ✅

## Open Questions

1. Should `.down.sql` migrations be required from the start, or only `.up.sql`? **Resolved:** Both `.up.sql` and `.down.sql` provided from the start.
2. Should seed/reference data (e.g. the initial FODMAP ingredient list) live in a migration, or stay as application-level idempotent seed logic? **Resolved:** Seed logic stays in Go code (`fodmap/store.Seed()`) — not as a migration.
3. Do we want the server to auto-run migrations on boot, or keep it as a separate `start.sh` step? **Resolved:** Separate `start.sh` step (`go run . db migrate-up`) before server starts.
4. Should the `river` migrations continue to live under the `river` schema and be handled separately by `river migrate-up`, or be folded into the central migration set? **Resolved:** River's own tables remain separate, managed by `river migrate-up`.
5. The baseline-migration approach — forcing vs. `IF NOT EXISTS`? **Resolved:** Baseline uses `IF NOT EXISTS` on all statements so it's safe for both fresh and existing databases. `db migrate-force` available for existing databases that prefer force-marking.

## Verification: Keeping the River Workflow Intact

There are currently **no automated tests** for the River migration workflow. `menutracking migrate-up` is only exercised manually through `start.sh`, and `make check` (lint + unit test + build) will not catch a broken River setup because unit tests use mocks.

To verify the River workflow remains intact after migration restructuring:

1. **Add an integration test** that connects to a real Postgres instance, runs the full migration sequence (`river migrate-up` + domain table creation), and asserts that the expected River schema tables (`river_job`, `river_leader`, etc.) and domain tables exist afterwards. This test should live in `menutracking/store/migrate_test.go` and only run when a `POSTGRES_DSN` environment variable is set (similar to how `start.sh` gates the migration step).

2. **Add a `make integration` target** to the Makefile that runs this test with a live database, separate from `make check`.

3. **Manual smoke test** — after any migration restructuring, run `./start.sh` end-to-end and confirm the menutracking pipeline starts without errors.

This should be done **before** any migration refactoring begins, so that the integration test can validate the current behavior first and then confirm it still works after changes.

## Risks and Gaps

### 1. In-flight schema mutations (ALTER TABLE, DROP COLUMN, CREATE EXTENSION)

> **Resolved:** All historical ALTERs folded into the baseline migration (`000001_baseline.up.sql`) which contains the final state of all tables. `CREATE EXTENSION IF NOT EXISTS vector` is in its own migration (`000000_create_vector_extension.up.sql`) that runs before the baseline.

The plan's baseline migration (step 3) captures the current DDL, but the current code also runs **in-flight schema mutations** that happen after `CREATE TABLE`:

- **`auth/postgres_store.go:118-121`** — four `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` statements for `users.status`, `users.role`, `user_profiles.created_at`, `user_profiles.updated_at`. These are not in any `.sql` file — they are ad-hoc migrations baked into the Go constructor.
- **`search/postgres.go:81`** — `ALTER TABLE reviews DROP COLUMN IF EXISTS embedding;` — a destructive migration run on every boot.
- **`search/postgres.go:68` and `189`** — `CREATE EXTENSION IF NOT EXISTS vector;` — the `pgvector` extension must be installed before any vector columns can be created. This is a superuser operation on many hosted Postgres services (Supabase, Neon, etc.) and may fail if the app user lacks `CREATE` privilege on extensions.

**Gap:** The transition plan (step 2) says "lift verbatim into a migration file," but these ALTER and EXTENSION statements are not `CREATE TABLE`. They must go into **separate, subsequent migration files** — not lumped into the baseline. The baseline should represent the schema as it exists today *after* all historical ALTERs have been applied (i.e. the columns `status`, `role`, `created_at`, `updated_at` already exist on the tables). The ALTERs themselves should be recorded as earlier migrations that are already past on existing databases, or the baseline should include the final table definitions with those columns already present.

**Recommendation:** The baseline migration (`000001_baseline.up.sql`) should contain the **final state** of all tables (i.e. `users` already has `status` and `role` columns, `user_profiles` already has `created_at` and `updated_at`, `reviews` has no `embedding` column). The historical ALTERs that added those columns do not need their own migration files — they are folded into the baseline. The `CREATE EXTENSION IF NOT EXISTS vector` statement should be its own migration (`000000_create_vector_extension.up.sql`) that runs before the baseline, since extensions are database-level objects separate from table schemas. On existing databases, both would be force-marked as applied.

**Caveat:** Force-marking the baseline as applied assumes **every** production/staging instance has already booted the current code and therefore run all the historical mutations (the four `ADD COLUMN`s and the `reviews` `DROP COLUMN embedding`). An instance still running older code at cutover would be force-marked without those mutations ever executing. For the additive `ADD COLUMN`s and the unused `embedding` drop this is benign (the columns either already exist or are harmless), but the cutover runbook should state the precondition: all instances must be on current code before force-marking the baseline.

### 2. `search.EnsureSchema` and `search.EnsureFodmapSchema` are interface methods

> **Resolved:** Interface methods kept as no-ops (return `nil`) in all implementations, preserving backward compatibility. The `Searcher` interface is unchanged.

Both `EnsureSchema` and `EnsureFodmapSchema` are defined on the `Searcher` interface (`server/server.go:22-27`). They are called from `server.New()` and have **three production implementations** (`PostgresClient`, `WeaviateClient`, `PineconeClient`) plus **four distinct test mocks** — `MockSearcher` (`server/direct_fodmap_client_test.go:33`), `noOpSearcher` (`server/admin_ingredients_handler_test.go:77`), `chatMockSearcher` (`server/chat_handler_test.go:162`), and `handlersTestSearcher` (`server/handlers_test.go:47`). After migration, the Postgres implementation will be empty (DDL moves to `golang-migrate`), but the interface methods must remain until all callers are updated.

**Gap:** Step 7 says "delete the inline DDL from `auth.NewPostgresStore` and remove `search.(*PostgresClient).EnsureSchema` (and its call sites)." But removing the interface methods requires updating every implementation and all four test mocks — a blast radius of at least seven types across seven files. The plan should either:

- Keep the interface methods as no-ops (return `nil`) in all implementations, preserving backward compatibility and leaving the `Searcher` interface unchanged, or
- Remove the methods from the interface entirely, updating all implementations and call sites in one pass.

The safer approach is to keep them as no-ops initially, then remove them in a follow-up PR once the migration system is proven in production. The plan should state this explicitly.

### 3. `fodmap/store` seed logic has a startup ordering dependency

> **Resolved:** `start.sh` runs `db migrate-up` before the server starts, so tables exist before `seedAndReload` is called.

`server.New()` calls `s.catalogStore.EnsureSchema()` then `s.catalogStore.Seed()` inside `seedAndReload()` (`server/server.go:220-235`). After migration, `EnsureSchema` becomes a no-op (the catalog store no longer creates its own tables). But the seed logic (`IsSeeded` / `Seed` / `SetSeeded`) depends on `fodmap_catalog` and `fodmap_meta` tables existing.

**Gap:** If migrations run correctly, the tables will exist before `seedAndReload` is called, so this is fine. But if someone runs the server without running migrations first, the seed will fail with a "relation does not exist" error. The plan should note that `start.sh` must run `db migrate-up` **before** the server starts, and the server should either fail fast with a clear error message or the `seedAndReload` method should check for table existence.

### 4. `search.EnsureFodmapSchema` creates a different `fodmap_ingredients` table

The `search` package's `EnsureFodmapSchema` creates `fodmap_ingredients` with an `embedding vector(768)` column and an HNSW index, while `fodmap/store`'s schema creates `fodmap_catalog` (no embedding column). These are **two different tables** owned by two different packages. The plan's table inventory (line 44) notes this but the baseline migration needs to include both.

**Gap:** The baseline migration must include both `fodmap_catalog`/`fodmap_meta` (from `fodmap/store`) and `fodmap_ingredients` with its vector column and index (from `search`). The `CREATE EXTENSION IF NOT EXISTS vector` must also be included as a prerequisite migration. The current step 2 lists `search/postgres.go:66` as a source but should explicitly mention `EnsureFodmapSchema` (`search/postgres.go:186-200`) as a separate source for the `fodmap_ingredients` table and the `pgvector` extension.

### 5. No test coverage for schema-creation paths

The existing tests for `EnsureSchema` and `EnsureFodmapSchema` (`search/postgres_test.go:44-98`) use `sqlmock` to verify that specific `CREATE TABLE` / `CREATE EXTENSION` statements are executed. After migration, these tests will need to be rewritten or removed, because the PostgresClient will no longer execute DDL.

**Gap:** Step 8 says "Tests that currently call store constructors which auto-run `schema.sql` (or `EnsureSchema`) should instead call a test helper that runs `db.Migrate`." But this requires an actual Postgres connection, not a mock. The scope here is narrower than it first appears: **only `search/postgres_test.go` actually asserts DDL** (it has explicit `sqlmock.ExpectExec("CREATE TABLE ...")` / `CREATE EXTENSION` / `ALTER TABLE` expectations at lines 44-98). The `sqlmock`-based tests in `auth/postgres_store_test.go` do **not** test the constructor's DDL — they construct the store directly and assert only CRUD (`INSERT INTO users`, `UPDATE users SET status`), so they are unaffected by the migration and need no changes. The plan should specify what happens to the `search` tests specifically:

- Tests for `EnsureSchema`/`EnsureFodmapSchema` on `PostgresClient` become trivial (method returns `nil`), so the test should verify the no-op behavior.
- The `sqlmock.ExpectExec("CREATE TABLE ...")` / `CREATE EXTENSION` / `ALTER TABLE` assertions in `search/postgres_test.go` must be deleted.

### 6. `menutracking/store/schema.sql` uses `IF NOT EXISTS` but `auth` and `search` DDL does too

> **Resolved:** The baseline migration uses `IF NOT EXISTS` on all statements so it's safe against both fresh and existing databases. Subsequent migrations should use plain `CREATE TABLE`.

Step 3 and Open Question #5 discuss whether to keep `IF NOT EXISTS` in the baseline migration. The analysis focuses on `menutracking/store/schema.sql`, but **all four DDL sources** use `IF NOT EXISTS`:

- `auth/postgres_store.go:71-99` — `CREATE TABLE IF NOT EXISTS` for all four tables.
- `search/postgres.go:66-91` — `CREATE TABLE IF NOT EXISTS` for `reviews` and `review_chunks`.
- `search/postgres.go:190-198` — `CREATE TABLE IF NOT EXISTS` for `fodmap_ingredients`.
- `fodmap/store/sql/schema.sql` — `CREATE TABLE IF NOT EXISTS` for `fodmap_catalog` and `fodmap_meta`.
- `menutracking/store/schema.sql` — `CREATE TABLE IF NOT EXISTS` for all five tables plus indexes.

**Gap:** The baseline migration decision applies to all of these, not just `menutracking`. If we use `migrate force` to skip the baseline on existing databases, none of them need `IF NOT EXISTS`. If we keep `IF NOT EXISTS` in the baseline for safety, all of them need it. The plan should be explicit that the same approach applies uniformly.

### 7. The `seed.sql` data migration is underspecified

`fodmap/store/sql/seed.sql` contains parameterized INSERTs (`ON CONFLICT DO NOTHING`) that are executed by the Go `Seed()` method, not raw DDL. The plan says "move seed data insertion into a migration or make it idempotent via `ON CONFLICT DO NOTHING`," but Open Question #2 leaves this open.

**Risk:** If seed data goes into a migration, it will be run once by `golang-migrate` and never again — which means adding new ingredients requires a new migration every time. The current `Seed()` method is called on every server start and skips if `fodmap_meta.seeded = true`. Moving it to a migration changes the semantics: the seed becomes a one-time event tied to a migration version, not a runtime check.

**Recommendation:** Keep the seed logic in Go code (`Seed()` method) as application-level idempotent logic, not as a migration. The migration should only create the empty `fodmap_catalog` and `fodmap_meta` tables. The `Seed()` method remains the authoritative way to populate reference data, and it already handles idempotency via `ON CONFLICT DO NOTHING`. This is what the plan should recommend in Open Question #2.

### 8. Interface stability during transition

> **Resolved:** Phase 1 (additive) and Phase 2 (removal) were combined into one pass. Interface methods (`EnsureSchema`, `EnsureFodmapSchema`, `CatalogStore.EnsureSchema`, `MigrateUp`) were made no-ops rather than removed, preserving backward compatibility.

The `Searcher` interface (`server/server.go:22-27`) has `EnsureSchema` and `EnsureFodmapSchema`. The `CatalogStore` interface (`server/server.go:43` — note the in-memory implementation lives in `server/catalog_store.go`, but the interface itself is declared in `server.go`) has `EnsureSchema`. The `menutracking` `MigrateUp` function is called from `cli/menutracking_migrate.go`. Removing these in one PR creates a large blast radius: server initialization, CLI commands, the four test mocks listed in Gap #2, and interface implementations all change simultaneously.

**Gap:** The transition plan should be done in at least two phases:

1. **Phase 1 (additive, non-breaking):** Add `internal/db/migrate.go`, create `migrations/` directory, add `db migrate-up` CLI command, update `start.sh` to call it. Do NOT remove `EnsureSchema`, `EnsureFodmapSchema`, `auth.NewPostgresStore` DDL, or `menutracking MigrateUp` yet. Both systems coexist: `golang-migrate` runs first, then the idempotent `CREATE TABLE IF NOT EXISTS` statements run harmlessly.

2. **Phase 2 (removal):** Remove the old DDL paths (`EnsureSchema`, inline Go DDL, `menutracking MigrateUp`), make interface methods no-ops, delete `schema.sql` files, update tests. This can be a separate PR once Phase 1 is validated in production.

This two-phase approach eliminates the risk of the server failing to start if migrations haven't run — the old `IF NOT EXISTS` paths remain as a safety net during Phase 1.

### 9. `start.sh` currently runs `menutracking migrate-up` with error tolerance

> **Resolved:** `start.sh` now calls `go run . db migrate-up` and fails hard on error (no `|| echo "Warning: ..."` tolerance).

Line 88 of `start.sh` runs `menutracking migrate-up` with `|| echo "Warning: menutracking migrations failed (may already be up)"` — meaning the script continues even if the migration fails. This suggests the project has no strict expectation that migrations succeed before the server starts.

**Gap:** After adopting `golang-migrate`, the `db migrate-up` command should fail hard (non-zero exit) if migrations fail. The `|| echo "Warning: ..."` tolerance was appropriate for the `IF NOT EXISTS` idempotent model but is dangerous with versioned migrations. The plan should note that `start.sh` must be updated to fail on migration errors, not silently continue.