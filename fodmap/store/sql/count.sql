SELECT COUNT(*) AS total
FROM fodmap_catalog
{{.Where}}
