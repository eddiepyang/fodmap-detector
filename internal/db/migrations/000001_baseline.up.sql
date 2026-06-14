-- Baseline migration: captures the full schema as it exists today.
-- Every statement uses IF NOT EXISTS, so running this on an existing
-- database (via "db migrate-up") is safe and idempotent — it no-ops on
-- tables that already exist. Force-marking it as applied
-- (e.g. "db migrate-force 1") is an optional optimization to skip the
-- redundant pass, not a requirement.
-- On fresh databases, this runs normally and creates everything.

-- Auth tables (previously inlined in auth/postgres_store.go).

CREATE TABLE IF NOT EXISTS users (
    id         TEXT PRIMARY KEY,
    email      TEXT UNIQUE NOT NULL,
    password   TEXT NOT NULL,
    role       TEXT NOT NULL DEFAULT 'user',
    status     TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS user_profiles (
    user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    profile    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS conversations (
    id                 TEXT PRIMARY KEY,
    user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    business_id        TEXT NOT NULL,
    business_name      TEXT,
    title              TEXT NOT NULL,
    created_at         TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    review_context     TEXT,
    search_category    TEXT,
    search_city        TEXT,
    search_state       TEXT,
    search_description TEXT
);

CREATE TABLE IF NOT EXISTS messages (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role            TEXT NOT NULL,
    content         TEXT NOT NULL,
    sequence        INTEGER NOT NULL,
    created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id, sequence);

-- Search tables (previously inlined in search/postgres.go).
-- reviews has no embedding column (historical DROP COLUMN already applied).

CREATE TABLE IF NOT EXISTS reviews (
    review_id TEXT PRIMARY KEY,
    business_id TEXT,
    business_name TEXT,
    city TEXT,
    state TEXT,
    categories TEXT,
    stars FLOAT,
    text TEXT
);

CREATE TABLE IF NOT EXISTS review_chunks (
    chunk_id SERIAL PRIMARY KEY,
    review_id TEXT REFERENCES reviews(review_id) ON DELETE CASCADE,
    chunk_text TEXT,
    embedding vector(768)
);

CREATE INDEX IF NOT EXISTS idx_review_chunks_embedding ON review_chunks USING hnsw (embedding vector_cosine_ops);

-- FODMAP vector search table (previously inlined in search/postgres.go EnsureFodmapSchema).

CREATE TABLE IF NOT EXISTS fodmap_ingredients (
    ingredient TEXT PRIMARY KEY,
    level TEXT,
    groups TEXT[],
    notes TEXT,
    substitutions TEXT[],
    embedding vector(768)
);

CREATE INDEX IF NOT EXISTS idx_fodmap_embedding ON fodmap_ingredients USING hnsw (embedding vector_cosine_ops);

-- FODMAP catalog tables (previously in fodmap/store/sql/schema.sql).

CREATE TABLE IF NOT EXISTS fodmap_catalog (
    ingredient TEXT PRIMARY KEY,
    level TEXT NOT NULL,
    groups TEXT[] NOT NULL DEFAULT '{}',
    notes TEXT NOT NULL DEFAULT '',
    substitutions TEXT[] NOT NULL DEFAULT '{}',
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS fodmap_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Menutracking domain tables (previously in menutracking/store/schema.sql).

CREATE TABLE IF NOT EXISTS sources (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    url         TEXT NOT NULL,
    domain      TEXT NOT NULL,
    tier        TEXT NOT NULL DEFAULT 'gov',
    cron_schedule TEXT NOT NULL DEFAULT '@weekly',
    max_tokens  INTEGER NOT NULL DEFAULT 32000,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sources_domain ON sources(domain);

CREATE TABLE IF NOT EXISTS extraction_rules (
    id          TEXT PRIMARY KEY,
    domain      TEXT NOT NULL,
    selector    TEXT NOT NULL,
    fields      JSONB NOT NULL,
    status      TEXT NOT NULL DEFAULT 'proposed',
    proposed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at TIMESTAMPTZ,
    provenance  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_extraction_rules_domain_status ON extraction_rules(domain, status);

CREATE TABLE IF NOT EXISTS regulatory_updates (
    id              TEXT PRIMARY KEY,
    source_id       TEXT NOT NULL REFERENCES sources(id),
    source_url      TEXT NOT NULL,
    cas_number      TEXT,
    substance_name  TEXT NOT NULL,
    change_type     TEXT NOT NULL,
    description     TEXT NOT NULL,
    effective_date  DATE,
    raw_path        TEXT,
    extracted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_regulatory_updates_source ON regulatory_updates(source_id);
CREATE INDEX IF NOT EXISTS idx_regulatory_updates_cas ON regulatory_updates(cas_number) WHERE cas_number IS NOT NULL;

CREATE TABLE IF NOT EXISTS menutracking_dead_letter (
    id          BIGSERIAL PRIMARY KEY,
    job_kind    TEXT NOT NULL,
    job_args    JSONB NOT NULL,
    error       TEXT NOT NULL,
    discarded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);