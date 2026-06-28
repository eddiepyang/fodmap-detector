UPDATE restaurants
SET extraction_tier = NULLIF($2, ''),
    updated_at = NOW()
WHERE camis = $1;
