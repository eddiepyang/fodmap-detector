# Database Schema

The authoritative DDL for all Postgres domain tables lives in `internal/db/migrations/` — versioned SQL files applied by `golang-migrate`. This document provides a quick reference of what each table is for. For column-level detail, read the migration files directly.

## Domain Tables

| Table | Purpose | Owned by |
|---|---|---|
| `users` | Authenticated user accounts | `auth` |
| `user_profiles` | JSON dietary preferences per user | `auth` |
| `conversations` | Chat conversation metadata | `auth` |
| `messages` | Individual chat messages within conversations | `auth` |
| `reviews` | Yelp review metadata (no embedding column) | `search` |
| `review_chunks` | Chunked review text with 768-dim vector embeddings | `search` |
| `fodmap_ingredients` | FODMAP vector search index (768-dim embeddings) | `search` |
| `fodmap_catalog` | Canonical FODMAP ingredient metadata (no vectors) | `fodmap/store` |
| `fodmap_meta` | Key/value metadata (e.g. seeded marker) | `fodmap/store` |
| `sources` | Regulatory source URLs and schedules | `menutracking` |
| `extraction_rules` | CSS/JSON-path extraction rules per domain | `menutracking` |
| `regulatory_updates` | Scraped regulatory changes | `menutracking` |
| `menutracking_dead_letter` | Audit trail for discarded river jobs | `menutracking` |

## Non-Domain Tables

River's own tables (`river_job`, `river_leader`, etc.) are managed separately by `river migrate-up` and live in the `river` schema. They are **not** included in the `internal/db/migrations/` directory.

## Running Migrations

```bash
# Run all pending domain + river migrations
go run . db migrate-up

# Check current version
go run . db migrate-version

# Existing database? Force-mark the baseline as already-applied:
go run . db migrate-force 1
```

## Adding a New Migration

1. Create a new numbered `.up.sql` and `.down.sql` file in `internal/db/migrations/` (e.g. `000002_add_column.up.sql`).
2. Use plain `CREATE TABLE` / `ALTER TABLE` (no `IF NOT EXISTS`) — `golang-migrate` guarantees each version runs exactly once.
3. Run `go run . db migrate-up` to apply.

## See Also

- [Data Model Guide](data-model.md) for application-level Go structs
- [Testing Guide](testing.md) for test patterns