SELECT 
  c.id, c.user_id, u.email, c.title, c.business_id, c.business_name,
  COALESCE((SELECT COUNT(*) FROM messages WHERE conversation_id = c.id), 0) AS message_count,
  c.created_at, c.updated_at
FROM conversations c
JOIN users u ON c.user_id = u.id
WHERE ($1 = '' OR c.title ILIKE '%' || $1 || '%' OR u.email ILIKE '%' || $1 || '%')
ORDER BY c.updated_at DESC
LIMIT $2 OFFSET $3
