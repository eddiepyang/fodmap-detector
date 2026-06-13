SELECT
    (SELECT COUNT(*) FROM fodmap_catalog) AS total_count,
    (SELECT jsonb_object_agg(level, cnt) FROM (
        SELECT level, COUNT(*) AS cnt
        FROM fodmap_catalog
        GROUP BY level
    ) levels) AS level_counts,
    (SELECT jsonb_object_agg(group_name, cnt) FROM (
        SELECT unnest(groups) AS group_name, COUNT(*) AS cnt
        FROM fodmap_catalog
        GROUP BY unnest(groups)
    ) groups) AS group_count
