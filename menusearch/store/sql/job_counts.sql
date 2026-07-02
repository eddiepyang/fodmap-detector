-- The sanitized, schema-qualified river_job identifier, interpolated at
-- query time because the River schema is configurable via --river-schema.
-- Finalized jobs (completed/discarded/cancelled) are limited to the last 24
-- hours so long-lived deployments don't drown live queue state in history.
SELECT kind, state::text, count(*)::int
FROM %s
WHERE kind IN ('menusearch.discover_menu_url', 'menusearch.scrape_menu')
  AND (finalized_at IS NULL OR finalized_at > now() - interval '24 hours')
GROUP BY kind, state;
