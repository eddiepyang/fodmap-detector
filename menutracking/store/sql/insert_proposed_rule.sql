-- name: insert-proposed-rule
-- Insert a proposed extraction rule. If a rule with the same ID already
-- exists (deterministic from domain+selector), update it.
INSERT INTO extraction_rules (id, domain, selector, fields, status, provenance, proposed_at, activated_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
  selector = EXCLUDED.selector,
  fields = EXCLUDED.fields,
  status = EXCLUDED.status,
  provenance = EXCLUDED.provenance,
  proposed_at = EXCLUDED.proposed_at;