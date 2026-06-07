-- name: upsert-regulatory-update
-- Insert or update a regulatory update using a deterministic ID derived from
-- source_id + date + cas_number + substance_name for idempotency.
INSERT INTO regulatory_updates (id, source_id, source_url, cas_number, substance_name, change_type, description, effective_date, raw_path, extracted_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (id) DO UPDATE SET
  source_url = EXCLUDED.source_url,
  substance_name = EXCLUDED.substance_name,
  change_type = EXCLUDED.change_type,
  description = EXCLUDED.description,
  effective_date = EXCLUDED.effective_date,
  extracted_at = EXCLUDED.extracted_at;