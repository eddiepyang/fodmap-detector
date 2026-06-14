UPDATE fodmap_catalog
SET level = $2,
    groups = $3,
    notes = $4,
    substitutions = $5,
    updated_at = CURRENT_TIMESTAMP
WHERE ingredient = $1
