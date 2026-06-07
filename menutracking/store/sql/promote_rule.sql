-- name: promote-rule
-- Promote a proposed rule to active after verification.
UPDATE extraction_rules
SET status = 'active', activated_at = $1
WHERE id = $2 AND status = 'proposed';