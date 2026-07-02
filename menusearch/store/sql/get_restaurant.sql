SELECT
    id, camis, yelp_id, dba, boro, building, street, zipcode, phone, address, cuisine, latitude, longitude, nta,
    status, website_url, menu_urls, url_source, extraction_tier, item_count, scraped_at, last_error, created_at, updated_at
FROM restaurants
WHERE camis = $1;
