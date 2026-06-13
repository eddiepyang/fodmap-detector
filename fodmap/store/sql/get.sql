SELECT ingredient, level, groups, notes, substitutions, updated_at
FROM fodmap_catalog
WHERE ingredient = $1
