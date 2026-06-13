SELECT COUNT(*)
FROM conversations c
JOIN users u ON c.user_id = u.id
WHERE ($1 = '' OR c.title ILIKE '%' || $1 || '%' OR u.email ILIKE '%' || $1 || '%')
