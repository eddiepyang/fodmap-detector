-- name: count
-- Count ingredients matching optional filters. Parameters are positional and always bound:
--   $1 = search pattern (ILIKE), pass '%%' when no search filter
--   $2 = level, pass '' when no level filter
--   $3 = group, pass '' when no group filter
SELECT COUNT(*) AS total
FROM fodmap_catalog
WHERE ($1 = '%%' OR ingredient ILIKE $1 OR notes ILIKE $1)
  AND ($2 = '' OR level = $2)
  AND ($3 = '' OR $3 = ANY(groups))