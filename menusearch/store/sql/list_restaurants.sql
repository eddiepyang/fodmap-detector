SELECT
    id, camis, yelp_id, dba, boro, building, street, zipcode, phone, address, cuisine, latitude, longitude, nta,
    status, website_url, menu_urls, url_source, extraction_tier, item_count, scraped_at, last_error, created_at, updated_at
FROM restaurants
WHERE ($1::text = '' OR status = $1)
  AND ($2::text = '' OR dba ILIKE '%' || $2 || '%')
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;
