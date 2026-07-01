# Database Schema

The authoritative DDL for all Postgres domain tables lives in `internal/db/migrations/` — versioned SQL files applied by `golang-migrate`. This document provides a quick reference of what each table is for. For column-level detail, read the migration files directly.

> **Migration 000010 (schema-consistency)** landed 2026-06-30 and was a large structural migration: `restaurants` gained a surrogate UUID primary key (`id`), `camis` was demoted to a nullable `UNIQUE` column, and `business_id` on `menu_items` / `reviews` / `conversations` was changed from a free-form `TEXT` column to a `UUID` foreign key referencing `restaurants(id)`. Several tables gained `created_at`/`updated_at` columns, `menu_items.scraped_at_utc TEXT` became `scraped_at TIMESTAMPTZ`, and all timestamp columns were converted from `TIMESTAMP` to `TIMESTAMPTZ`. A `touch_updated_at()` trigger now maintains `updated_at` on mutable tables. Embedding columns were converted from `vector(768)` to `halfvec(768)` in migration 000008. See [docs/plans/schema-consistency-surrogate-pk-plan.md](../plans/schema-consistency-surrogate-pk-plan.md) for the design rationale.

## Domain Tables

| Table | Purpose | Owned by |
|---|---|---|
| `users` | Authenticated user accounts | `auth` |
| `user_profiles` | JSON dietary preferences per user | `auth` |
| `conversations` | Chat conversation metadata | `auth` |
| `messages` | Individual chat messages within conversations | `auth` |
| `reviews` | Yelp review metadata (no embedding column); `business_id UUID → restaurants(id)` | `search` |
| `review_chunks` | Chunked review text with `halfvec(768)` embeddings | `search` |
| `fodmap_ingredients` | FODMAP vector search index (`halfvec(768)` embeddings) | `search` |
| `fodmap_catalog` | Canonical FODMAP ingredient metadata (no vectors) | `fodmap/store` |
| `fodmap_meta` | Key/value metadata (e.g. seeded marker) | `fodmap/store` |
| `restaurants` | NYC OpenData restaurant metadata; surrogate UUID PK, `camis` and `yelp_id` as external unique IDs | `menusearch` |
| `menu_items` | Vectorized menu item extraction results; `business_id UUID → restaurants(id)` | `menusearch` |
| `sources` | Regulatory source URLs and schedules | `menutracking` |
| `extraction_rules` | CSS/JSON-path extraction rules per domain | `menutracking` |
| `regulatory_updates` | Scraped regulatory changes | `menutracking` |
| `menutracking_dead_letter` | Audit trail for discarded river jobs | `menutracking` |

### Relationship diagram

```
              restaurants.id  (UUID, surrogate PK)
                    │
       ┌────────────┼────────────────────────────┐
       │            │                            │
  menu_items     reviews                  conversations
  business_id    business_id              business_id
  (UUID NOT NULL) (UUID, nullable)        (UUID NOT NULL)
```

Every `business_id` column is now a `UUID` FK to `restaurants(id)` (added in migration 000010). The external NYC CAMIS identifier lives on `restaurants.camis` (nullable, `UNIQUE`); the Yelp business ID lives on `restaurants.yelp_id` (nullable, `UNIQUE`). Application code and HTTP path params still use `camis` to identify a restaurant; the surrogate `id` is the join key for the `menu_items`/`reviews`/`conversations` tables.

## Non-Domain Tables

River's own tables (`river_job`, `river_leader`, `river_queue`, `river_client`, `river_migration`) are managed separately by `river migrate-up` and live in the `river` schema (configurable via `--river-schema`). They are **not** included in the `internal/db/migrations/` directory. The `db migrate-up` command creates the schema if missing and runs River's migrator into it.

> **Existing deployments:** `db migrate-up` detects river tables left in `public` and hard-errors with the one-time `ALTER TABLE ... SET SCHEMA river` steps. See [docs/plans/river-schema-and-dual-write-plan.md](../plans/river-schema-and-dual-write-plan.md).

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

1. Create a new numbered `.up.sql` and `.down.sql` file in `internal/db/migrations/` (e.g. `000011_add_column.up.sql`).
2. Use plain `CREATE TABLE` / `ALTER TABLE` (no `IF NOT EXISTS`) — `golang-migrate` guarantees each version runs exactly once.
3. Run `go run . db migrate-up` to apply.

## See Also

- [Data Model Guide](data-model.md) for application-level Go structs
- [Testing Guide](testing.md) for test patterns
- [Schema Consistency Plan](../plans/schema-consistency-surrogate-pk-plan.md) for the 000010 migration design

## Common Queries

> `business_id` on `menu_items`, `reviews`, and `conversations` is now a `UUID` referencing `restaurants(id)` — not the legacy string CAMIS/Yelp ID. Join on `restaurants.id`, and filter by external IDs via `restaurants.camis` or `restaurants.yelp_id`.

### Restaurant by external ID

```sql
-- by NYC CAMIS (legacy external ID; still what HTTP path params use)
SELECT id, camis, dba, status, website_url, menu_urls
FROM restaurants WHERE camis = '50044186';

-- by surrogate UUID PK (the join key)
SELECT id, camis, yelp_id, dba, status
FROM restaurants WHERE id = '550e8400-e29b-41d4-a716-446655440000';
```

### Menu items for a restaurant (by CAMIS)

```sql
SELECT m.menu_item_id, m.dish_name, m.price, m.menu_section, m.stated_ingredients,
       m.has_full_ingredients, m.source_url, m.scraped_at
FROM menu_items m
JOIN restaurants r ON m.business_id = r.id
WHERE r.camis = '50044186';
```

### Reviews for a restaurant (by CAMIS)

```sql
SELECT rv.review_id, rv.stars, rv.text, rv.created_at
FROM reviews rv
JOIN restaurants r ON rv.business_id = r.id
WHERE r.camis = '50044186';
```

### Conversations for a restaurant

```sql
SELECT c.id, c.title, c.created_at, c.updated_at
FROM conversations c
JOIN restaurants r ON c.business_id = r.id
WHERE r.camis = '50044186';
```

### Pipeline status rollup

```sql
SELECT status, count(*) FROM restaurants GROUP BY 1 ORDER BY 1;
```

### Failure taxonomy

```sql
SELECT last_error, count(*) FROM restaurants
WHERE status = 'failed_scrape'
GROUP BY last_error ORDER BY count(*) DESC;
```

### Tier mix

```sql
SELECT COALESCE(extraction_tier, '(none)') AS tier,
       count(*), COALESCE(sum(item_count), 0) AS items
FROM restaurants WHERE status = 'scraped'
GROUP BY 1 ORDER BY 2 DESC;
```

### Stuck/retryable River jobs

```sql
-- Discover jobs stuck in retryable state
SELECT id, args, attempts, finalized_at
FROM river.river_job
WHERE kind = 'menusearch.discover_menu_url' AND state = 'retryable';

-- Scrape jobs stuck in retryable state
SELECT id, args, attempts, finalized_at
FROM river.river_job
WHERE kind = 'menusearch.scrape_menu' AND state = 'retryable';
```

> The `river_job` table lives in the `river` schema by default (configurable via `--river-schema`). Qualify it as `river.river_job` or set `search_path` accordingly.

## Actual Schemas (Current State)

Below are the current schema definitions (with migrations 000000–000010 applied) for quick reference. Constraints, indices, and triggers are defined in the migration files.

### Auth

**`users`**

| Column | Type | Default / Constraints |
|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY` |
| `email` | `TEXT` | `UNIQUE NOT NULL` |
| `password` | `TEXT` | `NOT NULL` |
| `role` | `TEXT` | `NOT NULL DEFAULT 'user'` |
| `status` | `TEXT` | `NOT NULL DEFAULT 'active'` |
| `created_at` | `TIMESTAMPTZ` | `DEFAULT NOW()` |
| `updated_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |

Trigger: `trg_users_updated_at` (maintains `updated_at`).

**`user_profiles`**

| Column | Type | Default / Constraints |
|---|---|---|
| `user_id` | `TEXT` | `PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE` |
| `profile` | `JSONB` | `NOT NULL DEFAULT '{}'::jsonb` |
| `created_at` | `TIMESTAMPTZ` | `DEFAULT NOW()` |
| `updated_at` | `TIMESTAMPTZ` | `DEFAULT NOW()` |

Trigger: `trg_user_profiles_updated_at`.

**`conversations`**

| Column | Type | Default / Constraints |
|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY` |
| `user_id` | `TEXT` | `NOT NULL REFERENCES users(id) ON DELETE CASCADE` |
| `business_id` | `UUID` | `NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE` |
| `business_name` | `TEXT` | |
| `title` | `TEXT` | `NOT NULL` |
| `created_at` | `TIMESTAMPTZ` | `DEFAULT NOW()` |
| `updated_at` | `TIMESTAMPTZ` | `DEFAULT NOW()` |
| `review_context` | `TEXT` | |
| `search_category` | `TEXT` | |
| `search_city` | `TEXT` | |
| `search_state` | `TEXT` | |
| `search_description` | `TEXT` | |

Trigger: `trg_conversations_updated_at`.

> **Breaking change (000010):** `business_id` changed from `TEXT NOT NULL` (free-form string) to `UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE`. The legacy string `"general"` sentinel can no longer be stored. Existing conversations were truncated during the migration.

**`messages`**

| Column | Type | Default / Constraints |
|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY` |
| `conversation_id` | `TEXT` | `NOT NULL REFERENCES conversations(id) ON DELETE CASCADE` |
| `role` | `TEXT` | `NOT NULL` |
| `content` | `TEXT` | `NOT NULL` |
| `sequence` | `INTEGER` | `NOT NULL` |
| `created_at` | `TIMESTAMPTZ` | `DEFAULT NOW()` |

Index: `idx_messages_conversation (conversation_id, sequence)`.

### Search & FODMAP

**`reviews`**

| Column | Type | Default / Constraints |
|---|---|---|
| `review_id` | `TEXT` | `PRIMARY KEY` |
| `business_id` | `UUID` | `REFERENCES restaurants(id) ON DELETE CASCADE` (nullable) |
| `business_name` | `TEXT` | |
| `city` | `TEXT` | |
| `state` | `TEXT` | |
| `categories` | `TEXT` | |
| `stars` | `FLOAT` | |
| `text` | `TEXT` | |
| `created_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |

Index: `idx_reviews_business_id (business_id)`.

> **Breaking change (000010):** `business_id` changed from `TEXT` to `UUID REFERENCES restaurants(id)`. Reviews whose Yelp `business_id` has no matching `restaurants.yelp_id` row have `business_id = NULL` (the FK is nullable). Existing reviews were truncated during the migration; re-index from the JSONL archive to repopulate.

**`review_chunks`**

| Column | Type | Default / Constraints |
|---|---|---|
| `chunk_id` | `SERIAL` | `PRIMARY KEY` |
| `review_id` | `TEXT` | `REFERENCES reviews(review_id) ON DELETE CASCADE` |
| `chunk_text` | `TEXT` | |
| `embedding` | `halfvec(768)` | |
| `created_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |

Index: `idx_review_chunks_embedding` HNSW with `halfvec_cosine_ops`.

> **Migration 000008:** embedding column type changed from `vector(768)` to `halfvec(768)` (float16 storage, ~2× HNSW scan speed). The HNSW index was dropped and recreated with `halfvec_cosine_ops`.

**`fodmap_ingredients`**

| Column | Type | Default / Constraints |
|---|---|---|
| `ingredient` | `TEXT` | `PRIMARY KEY` |
| `level` | `TEXT` | |
| `groups` | `TEXT[]` | |
| `notes` | `TEXT` | |
| `substitutions` | `TEXT[]` | |
| `embedding` | `halfvec(768)` | |
| `created_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |

Index: `idx_fodmap_embedding` HNSW with `halfvec_cosine_ops`.

**`fodmap_catalog`**

| Column | Type | Default / Constraints |
|---|---|---|
| `ingredient` | `TEXT` | `PRIMARY KEY` |
| `level` | `TEXT` | `NOT NULL` |
| `groups` | `TEXT[]` | `NOT NULL DEFAULT '{}'` |
| `notes` | `TEXT` | `NOT NULL DEFAULT ''` |
| `substitutions` | `TEXT[]` | `NOT NULL DEFAULT '{}'` |
| `created_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |
| `updated_at` | `TIMESTAMPTZ` | `DEFAULT NOW()` |

Trigger: `trg_fodmap_catalog_updated_at`.

**`fodmap_meta`**

| Column | Type | Default / Constraints |
|---|---|---|
| `key` | `TEXT` | `PRIMARY KEY` |
| `value` | `TEXT` | `NOT NULL` |

### Menu Search

**`restaurants`**

| Column | Type | Default / Constraints |
|---|---|---|
| `id` | `UUID` | `PRIMARY KEY DEFAULT gen_random_uuid()` |
| `camis` | `TEXT` | `UNIQUE` (nullable — external NYC DOHMH ID) |
| `yelp_id` | `TEXT` | `UNIQUE` (nullable — external Yelp business ID) |
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
| `extraction_tier` | `TEXT` | (nullable — set by scrape pipeline) |
| `item_count` | `INTEGER` | `DEFAULT 0` |
| `scraped_at` | `TIMESTAMPTZ` | |
| `last_error` | `TEXT` | |
| `created_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |
| `updated_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |

Indices: `idx_restaurants_status (status)`, `idx_restaurants_dba` (GIN on `to_tsvector('english', dba)`), `idx_restaurants_nta (nta)`, `restaurants_camis_unique (camis)`, `yelp_id` unique.

Trigger: `trg_restaurants_updated_at`.

> **Breaking change (000010):** `camis` was demoted from `PRIMARY KEY` to a nullable `UNIQUE` column; the new surrogate PK is `id UUID DEFAULT gen_random_uuid()`. The `restaurants_pkey CASCADE` constraint was dropped during migration, which cascaded to FK re-creation on `menu_items`/`reviews`/`conversations`. All four tables now join via `business_id UUID → restaurants.id`. The HTTP API and CLI still accept `{camis}` path params; the server resolves `camis → id` internally.

**`menu_items`**

| Column | Type | Default / Constraints |
|---|---|---|
| `menu_item_id` | `TEXT` | `PRIMARY KEY` |
| `business_id` | `UUID` | `NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE` |
| `menu_section` | `TEXT` | |
| `restaurant_name` | `TEXT` | |
| `city` | `TEXT` | |
| `state` | `TEXT` | |
| `dish_name` | `TEXT` | `NOT NULL` |
| `description` | `TEXT` | |
| `price` | `NUMERIC(10,2)` | (added in 000007) |
| `stated_ingredients` | `TEXT[]` | |
| `has_full_ingredients` | `BOOLEAN` | `NOT NULL DEFAULT FALSE` |
| `modifiers` | `JSONB` | `DEFAULT '[]'::jsonb` (added in 000007) |
| `source_url` | `TEXT` | |
| `address` | `TEXT` | |
| `phone_number` | `TEXT` | |
| `scraped_at` | `TIMESTAMPTZ` | (renamed from `scraped_at_utc TEXT` in 000010) |
| `embedding` | `halfvec(768)` | |
| `created_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` (added in 000010) |
| `updated_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` (added in 000010) |

Indices: `idx_menu_items_embedding` HNSW with `halfvec_cosine_ops`, `idx_menu_items_business_id (business_id)`.

Trigger: `trg_menu_items_updated_at` (the `ON CONFLICT DO UPDATE` clause in the upsert deliberately omits `updated_at` — the trigger owns it).

> **Breaking change (000010):** `business_id` changed from `TEXT NOT NULL` (legacy camis-derived or URL-derived string) to `UUID NOT NULL REFERENCES restaurants(id)`. The table was truncated and re-scraping is required to repopulate with UUID `business_id` values. `scraped_at_utc TEXT` was renamed to `scraped_at` and converted to `TIMESTAMPTZ`.

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

Indices: `idx_sources_domain (domain)`.

Trigger: `trg_sources_updated_at`.

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
| `updated_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` (added in 000010) |

Index: `idx_extraction_rules_domain_status (domain, status)`.

Trigger: `trg_extraction_rules_updated_at`.

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

Indices: `idx_regulatory_updates_source (source_id)`, `idx_regulatory_updates_cas (cas_number) WHERE cas_number IS NOT NULL`.

**`menutracking_dead_letter`**

| Column | Type | Default / Constraints |
|---|---|---|
| `id` | `BIGSERIAL` | `PRIMARY KEY` |
| `job_kind` | `TEXT` | `NOT NULL` |
| `job_args` | `JSONB` | `NOT NULL` |
| `error` | `TEXT` | `NOT NULL` |
| `discarded_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` |

### Shared Trigger Function

Defined in migration 000010 and used by all `trg_*_updated_at` triggers:

```sql
CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
```

Tables with a `trg_*_updated_at BEFORE UPDATE` trigger: `users`, `user_profiles`, `conversations`, `restaurants`, `sources`, `extraction_rules`, `menu_items`, `fodmap_catalog`. Application code that upserts into these tables should **not** set `updated_at` in the `ON CONFLICT DO UPDATE` clause — the trigger owns it.