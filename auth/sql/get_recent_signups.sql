SELECT id, email, role, status, created_at
FROM users
WHERE status != 'deleted'
ORDER BY created_at DESC
LIMIT 5
