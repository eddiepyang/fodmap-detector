SELECT extraction_tier, count(*)::int, COALESCE(SUM(item_count), 0)::int AS items
FROM restaurants
WHERE status = 'scraped' AND extraction_tier IS NOT NULL AND extraction_tier <> ''
GROUP BY extraction_tier;
