UPDATE restaurants
SET menu_url = NULLIF($2, ''),
    menu_url_source = NULLIF($3, ''),
    status = CASE 
        WHEN NULLIF($2, '') IS NOT NULL THEN 'url_found' 
        ELSE 'no_url_found' 
    END,
    updated_at = NOW()
WHERE camis = $1;
