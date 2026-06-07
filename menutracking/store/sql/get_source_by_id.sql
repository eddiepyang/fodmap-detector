-- name: get-source-by-id
-- Get a single source by its ID.
SELECT id, name, url, domain, tier, cron_schedule, max_tokens, created_at, updated_at
FROM sources
WHERE id = $1;