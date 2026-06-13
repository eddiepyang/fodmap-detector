SELECT COUNT(*) FROM users
WHERE status != 'deleted'
  AND ($1 = '' OR email ILIKE '%' || $1 || '%')
  AND ($2 = '' OR status = $2)
