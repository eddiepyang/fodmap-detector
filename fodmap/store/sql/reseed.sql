INSERT INTO fodmap_catalog (ingredient, level, groups, notes, substitutions)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (ingredient) DO UPDATE SET
    level = EXCLUDED.level,
    groups = EXCLUDED.groups,
    notes = EXCLUDED.notes,
    substitutions = EXCLUDED.substitutions,
    updated_at = CURRENT_TIMESTAMP