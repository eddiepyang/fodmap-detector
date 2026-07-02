UPDATE restaurants
SET website_url = NULLIF($2::text, ''),
    menu_urls = $3::text[],
    url_source = NULLIF($4::text, ''),
    address = COALESCE(NULLIF($5::text, ''), address),
    phone = COALESCE(NULLIF($6::text, ''), phone),
    status = CASE 
        WHEN NULLIF($2::text, '') IS NOT NULL OR array_length($3::text[], 1) > 0 THEN 'url_found' 
        ELSE 'failed_permanently' 
    END
WHERE camis = $1;
