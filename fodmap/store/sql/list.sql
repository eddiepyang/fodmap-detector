SELECT ingredient, level, groups, notes, substitutions, updated_at
FROM fodmap_catalog
{{.Where}}
ORDER BY ingredient ASC
LIMIT {{.LimitArg}} OFFSET {{.OffsetArg}}
