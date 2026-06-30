UPDATE restaurants
SET extraction_tier = NULLIF($2, '')
WHERE camis = $1;
