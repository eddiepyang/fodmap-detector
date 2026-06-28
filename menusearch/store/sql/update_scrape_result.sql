UPDATE restaurants
SET status = $2,
    item_count = $3,
    last_error = NULLIF($4, ''),
    scraped_at = CASE WHEN $2 = 'scraped' THEN NOW() ELSE scraped_at END,
    updated_at = NOW()
WHERE camis = $1
  AND NOT (status = 'scraped' AND $2 = 'failed_scrape');
