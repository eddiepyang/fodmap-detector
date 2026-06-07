-- name: insert-source
-- Upsert a source. The ID is generated deterministically from the domain
-- so re-inserts are idempotent.
INSERT INTO sources (id, name, url, domain, tier, cron_schedule, max_tokens, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
  name = EXCLUDED.name,
  url = EXCLUDED.url,
  domain = EXCLUDED.domain,
  tier = EXCLUDED.tier,
  cron_schedule = EXCLUDED.cron_schedule,
  max_tokens = EXCLUDED.max_tokens,
  updated_at = EXCLUDED.updated_at;