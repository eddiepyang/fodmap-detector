SELECT status, count(*)::int AS count
FROM restaurants
GROUP BY status;
