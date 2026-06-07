-- name: get-proposed-rule
-- Get a proposed extraction rule by its ID.
SELECT id, domain, selector, fields, status, provenance, proposed_at, activated_at, created_at
FROM extraction_rules
WHERE id = $1 AND status = 'proposed';