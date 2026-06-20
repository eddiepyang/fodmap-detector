-- name: list
-- List ingredients with optional filters. Parameters are positional and always bound:
--   $1 = search pattern (ILIKE), pass '%%' when no search filter
--   $2 = level, pass '' when no level filter
--   $3 = group, pass '' when no group filter
--   $4 = LIMIT
--   $5 = OFFSET
SELECT ingredient, level, groups, notes, substitutions, updated_at
FROM fodmap_catalog
WHERE ($1 = '%%' OR ingredient ILIKE $1 OR notes ILIKE $1)
  AND ($2 = '' OR level = $2)
  AND ($3 = '' OR $3 = ANY(groups))
ORDER BY ingredient ASC
LIMIT $4 OFFSET $5