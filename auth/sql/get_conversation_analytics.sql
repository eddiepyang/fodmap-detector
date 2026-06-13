SELECT 
  COUNT(*),
  COALESCE(COUNT(*)::float / NULLIF((SELECT COUNT(*) FROM users WHERE status != 'deleted'), 0), 0.0)
FROM conversations
