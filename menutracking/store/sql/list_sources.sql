-- name: list-sources
-- List all sources ordered by domain.
SELECT id, name, url, domain, tier, cron_schedule, max_tokens, created_at, updated_at
FROM sources
ORDER BY domain;