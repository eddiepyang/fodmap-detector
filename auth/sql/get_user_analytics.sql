SELECT 
  COUNT(*), 
  COUNT(*) FILTER (WHERE status = 'active'), 
  COUNT(*) FILTER (WHERE status = 'suspended')
FROM users
WHERE status != 'deleted'
