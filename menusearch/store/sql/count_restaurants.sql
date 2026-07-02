SELECT count(*)::int
FROM restaurants
WHERE ($1::text = '' OR status = $1)
  AND ($2::text = '' OR dba ILIKE '%' || $2 || '%');
