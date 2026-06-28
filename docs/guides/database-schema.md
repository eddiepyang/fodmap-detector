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
| `restaurants` | NYC OpenData restaurant metadata (CAMIS, address, etc) | `menusearch` |
| `menu_items` | Vectorized menu item extraction results | `menusearch` |
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

## Actual Schemas (Current State)

Below are the current schema definitions (with migrations applied) for quick reference. Constraints and indices are defined in the migration files.

### Auth

**`users`**

| Column | Type | Default / Constraints |
|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY` |
| `email` | `TEXT` | `UNIQUE NOT NULL` |
| `password` | `TEXT` | `NOT NULL` |
| `role` | `TEXT` | `NOT NULL DEFAULT 'user'` |
| `status` | `TEXT` | `NOT NULL DEFAULT 'active'` |
| `created_at` | `TIMESTAMP` | `DEFAULT CURRENT_TIMESTAMP` |

**`user_profiles`**

| Column | Type | Default / Constraints |
|---|---|---|
| `user_id` | `TEXT` | `PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE` |
| `profile` | `JSONB` | `NOT NULL DEFAULT '{}'::jsonb` |
| `created_at` | `TIMESTAMP` | `DEFAULT CURRENT_TIMESTAMP` |
| `updated_at` | `TIMESTAMP` | `DEFAULT CURRENT_TIMESTAMP` |

**`conversations`**

| Column | Type | Default / Constraints |
|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY` |
| `user_id` | `TEXT` | `NOT NULL REFERENCES users(id) ON DELETE CASCADE` |
| `business_id` | `TEXT` | `NOT NULL` |
| `business_name` | `TEXT` | |
| `title` | `TEXT` | `NOT NULL` |
| `created_at` | `TIMESTAMP` | `DEFAULT CURRENT_TIMESTAMP` |
| `updated_at` | `TIMESTAMP` | `DEFAULT CURRENT_TIMESTAMP` |
| `review_context` | `TEXT` | |
| `search_category` | `TEXT` | |
| `search_city` | `TEXT` | |
| `search_state` | `TEXT` | |
| `search_description` | `TEXT` | |

**`messages`**

| Column | Type | Default / Constraints |
|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY` |
| `conversation_id` | `TEXT` | `NOT NULL REFERENCES conversations(id) ON DELETE CASCADE` |
| `role` | `TEXT` | `NOT NULL` |
| `content` | `TEXT` | `NOT NULL` |
| `sequence` | `INTEGER` | `NOT NULL` |
| `created_at` | `TIMESTAMP` | `DEFAULT CURRENT_TIMESTAMP` |

### Search & FODMAP

**`reviews`**

| Column | Type | Default / Constraints |
|---|---|---|
| `review_id` | `TEXT` | `PRIMARY KEY` |
| `business_id` | `TEXT` | |
| `business_name` | `TEXT` | |
| `city` | `TEXT` | |
| `state` | `TEXT` | |
| `categories` | `TEXT` | |
| `stars` | `FLOAT` | |
| `text` | `TEXT` | |

**`review_chunks`**

| Column | Type | Default / Constraints |
|---|---|---|
| `chunk_id` | `SERIAL` | `PRIMARY KEY` |
| `review_id` | `TEXT` | `REFERENCES reviews(review_id) ON DELETE CASCADE` |
| `chunk_text` | `TEXT` | |
| `embedding` | `vector(768)` | |

**`fodmap_ingredients`**

| Column | Type | Default / Constraints |
|---|---|---|
| `ingredient` | `TEXT` | `PRIMARY KEY` |
| `level` | `TEXT` | |
| `groups` | `TEXT[]` | |
| `notes` | `TEXT` | |
| `substitutions` | `TEXT[]` | |
| `embedding` | `vector(768)` | |

**`fodmap_catalog`**

| Column | Type | Default / Constraints |
|---|---|---|
| `ingredient` | `TEXT` | `PRIMARY KEY` |
| `level` | `TEXT` | `NOT NULL` |
| `groups` | `TEXT[]` | `NOT NULL DEFAULT '{}'` |
| `notes` | `TEXT` | `NOT NULL DEFAULT ''` |
| `substitutions` | `TEXT[]` | `NOT NULL DEFAULT '{}'` |
| `updated_at` | `TIMESTAMP` | `DEFAULT CURRENT_TIMESTAMP` |

**`fodmap_meta`**

| Column | Type | Default / Constraints |
|---|---|---|
| `key` | `TEXT` | `PRIMARY KEY` |
| `value` | `TEXT` | `NOT NULL` |

### Menu Search

**`restaurants`**

| Column | Type | Default / Constraints |
|---|---|---|
| `camis` | `TEXT` | `PRIMARY KEY` |
| `dba` | `TEXT` | `NOT NULL` |
| `boro` | `TEXT` | |
| `building` | `TEXT` | |
| `street` | `TEXT` | |
| `zipcode` | `TEXT` | |
| `phone` | `TEXT` | |
| `cuisine` | `TEXT` | |
| `latitude` | `DOUBLE PRECISION` | |
| `longitude` | `DOUBLE PRECISION` | |
| `nta` | `TEXT` | |
| `address` | `TEXT` | |
| `status` | `TEXT` | `NOT NULL DEFAULT 'pending_discovery'` |
| `website_url` | `TEXT` | |
| `url_source` | `TEXT` | |
| `menu_urls` | `TEXT[]` | `NOT NULL DEFAULT '{}'` |
| `item_count` | `INTEGER` | `DEFAULT 0` |
| `scraped_at` | `TIMESTAMPTZ` | |
| `last_error` | `TEXT` | |
| `created_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |
| `updated_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |

**`menu_items`**

| Column | Type | Default / Constraints |
|---|---|---|
| `menu_item_id` | `TEXT` | `PRIMARY KEY` |
| `business_id` | `TEXT` | `NOT NULL` |
| `menu_section` | `TEXT` | |
| `restaurant_name` | `TEXT` | |
| `city` | `TEXT` | |
| `state` | `TEXT` | |
| `dish_name` | `TEXT` | `NOT NULL` |
| `description` | `TEXT` | |
| `stated_ingredients` | `TEXT[]` | |
| `has_full_ingredients` | `BOOLEAN` | `NOT NULL DEFAULT FALSE` |
| `source_url` | `TEXT` | |
| `address` | `TEXT` | |
| `phone_number` | `TEXT` | |
| `scraped_at_utc` | `TEXT` | |
| `embedding` | `VECTOR(768)` | |

### Regulatory Menu Tracking

**`sources`**

| Column | Type | Default / Constraints |
|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY` |
| `name` | `TEXT` | `NOT NULL` |
| `url` | `TEXT` | `NOT NULL` |
| `domain` | `TEXT` | `NOT NULL` |
| `tier` | `TEXT` | `NOT NULL DEFAULT 'gov'` |
| `cron_schedule` | `TEXT` | `NOT NULL DEFAULT '@weekly'` |
| `max_tokens` | `INTEGER` | `NOT NULL DEFAULT 32000` |
| `created_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |
| `updated_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |

**`extraction_rules`**

| Column | Type | Default / Constraints |
|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY` |
| `domain` | `TEXT` | `NOT NULL` |
| `selector` | `TEXT` | `NOT NULL` |
| `fields` | `JSONB` | `NOT NULL` |
| `status` | `TEXT` | `NOT NULL DEFAULT 'proposed'` |
| `proposed_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |
| `activated_at` | `TIMESTAMPTZ` | |
| `provenance` | `TEXT` | `NOT NULL` |
| `created_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |

**`regulatory_updates`**

| Column | Type | Default / Constraints |
|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY` |
| `source_id` | `TEXT` | `NOT NULL REFERENCES sources(id)` |
| `source_url` | `TEXT` | `NOT NULL` |
| `cas_number` | `TEXT` | |
| `substance_name` | `TEXT` | `NOT NULL` |
| `change_type` | `TEXT` | `NOT NULL` |
| `description` | `TEXT` | `NOT NULL` |
| `effective_date` | `DATE` | |
| `raw_path` | `TEXT` | |
| `extracted_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |

**`menutracking_dead_letter`**

| Column | Type | Default / Constraints |
|---|---|---|
| `id` | `BIGSERIAL` | `PRIMARY KEY` |
| `job_kind` | `TEXT` | `NOT NULL` |
| `job_args` | `JSONB` | `NOT NULL` |
| `error` | `TEXT` | `NOT NULL` |
| `discarded_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |