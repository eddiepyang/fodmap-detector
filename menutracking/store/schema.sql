-- Regulatory tracking domain tables.
-- Run by: go run . menutracking migrate-up
-- River's own tables (river_job, river_leader, etc.) are managed by
-- river migrate-up and live in the 'river' schema.

CREATE TABLE IF NOT EXISTS sources (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    url         TEXT NOT NULL,
    domain      TEXT NOT NULL,
    tier        TEXT NOT NULL DEFAULT 'gov',       -- gov | consultancy | commercial
    cron_schedule TEXT NOT NULL DEFAULT '@daily',  -- robfig/cron-style, used by river PeriodicJob
    max_tokens  INTEGER NOT NULL DEFAULT 32000,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sources_domain ON sources(domain);

CREATE TABLE IF NOT EXISTS extraction_rules (
    id          TEXT PRIMARY KEY,
    domain      TEXT NOT NULL,
    selector    TEXT NOT NULL,                      -- CSS selector or JSON path
    fields      JSONB NOT NULL,                     -- field→path mappings
    status      TEXT NOT NULL DEFAULT 'proposed',   -- proposed | active | rejected
    proposed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at TIMESTAMPTZ,
    provenance  TEXT NOT NULL,                      -- URL of the page that generated this rule
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_extraction_rules_domain_status ON extraction_rules(domain, status);

CREATE TABLE IF NOT EXISTS regulatory_updates (
    id              TEXT PRIMARY KEY,                -- deterministic UUID: source_id + date + cas/identifier
    source_id       TEXT NOT NULL REFERENCES sources(id),
    source_url      TEXT NOT NULL,
    cas_number      TEXT,                             -- CAS Registry Number, if applicable
    substance_name  TEXT NOT NULL,
    change_type     TEXT NOT NULL,                    -- addition | restriction | revocation | update
    description     TEXT NOT NULL,
    effective_date  DATE,
    raw_path        TEXT,                             -- path under data/bronze/ with raw scraped content
    extracted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_regulatory_updates_source ON regulatory_updates(source_id);
CREATE INDEX IF NOT EXISTS idx_regulatory_updates_cas ON regulatory_updates(cas_number) WHERE cas_number IS NOT NULL;

-- Dead-letter audit for discarded river jobs that exceeded their MaxAttempts.
-- Written to from a river error handler hook so no regulatory_update is lost
-- even after the river_job row is eventually reaped.
CREATE TABLE IF NOT EXISTS menutracking_dead_letter (
    id          BIGSERIAL PRIMARY KEY,
    job_kind    TEXT NOT NULL,
    job_args    JSONB NOT NULL,
    error       TEXT NOT NULL,
    discarded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);