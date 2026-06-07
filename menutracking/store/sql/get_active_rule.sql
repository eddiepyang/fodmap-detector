-- name: get-active-rule
-- Get the active extraction rule for a domain, if one exists.
SELECT id, domain, selector, fields, status, provenance, proposed_at, activated_at, created_at
FROM extraction_rules
WHERE domain = $1 AND status = 'active'
LIMIT 1;