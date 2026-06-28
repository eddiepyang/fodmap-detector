UPDATE restaurants
SET website_url = NULLIF($2, ''),
    menu_urls = $3,
    url_source = NULLIF($4, ''),
    status = CASE 
        WHEN NULLIF($2, '') IS NOT NULL OR array_length($3, 1) > 0 THEN 'url_found' 
        ELSE 'no_url_found' 
    END,
    updated_at = NOW()
WHERE camis = $1;
