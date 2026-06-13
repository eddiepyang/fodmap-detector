SELECT id, email, role, status, created_at FROM users
WHERE status != 'deleted'
  AND ($1 = '' OR email ILIKE '%' || $1 || '%')
  AND ($2 = '' OR status = $2)
ORDER BY created_at DESC
LIMIT $3 OFFSET $4
