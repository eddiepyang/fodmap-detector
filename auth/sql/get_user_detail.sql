SELECT 
  u.id, u.email, u.role, u.status, u.created_at,
  COALESCE((SELECT COUNT(*) FROM conversations WHERE user_id = u.id), 0) AS conversation_count,
  COALESCE((SELECT COUNT(*) FROM messages m JOIN conversations c ON m.conversation_id = c.id WHERE c.user_id = u.id), 0) AS message_count,
  up.profile
FROM users u
LEFT JOIN user_profiles up ON up.user_id = u.id
WHERE u.id = $1 AND u.status != 'deleted'
