UPDATE restaurants
SET website_url = NULLIF($2::text, ''),
    menu_urls = $3::text[],
    url_source = NULLIF($4::text, ''),
    status = CASE 
        WHEN NULLIF($2::text, '') IS NOT NULL OR array_length($3::text[], 1) > 0 THEN 'url_found' 
        ELSE 'no_url_found' 
    END,
    updated_at = NOW()
WHERE camis = $1;
