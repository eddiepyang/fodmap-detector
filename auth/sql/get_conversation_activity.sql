SELECT TO_CHAR(created_at, 'YYYY-MM-DD') AS day, COUNT(*)
FROM conversations
WHERE created_at >= NOW() - ($1 * INTERVAL '1 day')
GROUP BY day
ORDER BY day ASC
