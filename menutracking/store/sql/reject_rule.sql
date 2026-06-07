-- name: reject-rule
-- Reject a proposed rule after it fails verification.
UPDATE extraction_rules
SET status = 'rejected'
WHERE id = $1 AND status = 'proposed';