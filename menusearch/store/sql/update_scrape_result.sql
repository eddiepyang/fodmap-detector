UPDATE restaurants
SET status = $2,
    item_count = $3,
    last_error = NULLIF($4, ''),
    scraped_at = CASE WHEN $2 = 'scraped' THEN NOW() ELSE scraped_at END,
    updated_at = NOW()
WHERE camis = $1
  -- Once a restaurant is successfully scraped, only another 'scraped' write may
  -- change it. This blocks BOTH a sibling menu-URL job's opening 'scraping'
  -- reset (which would zero item_count) and a later 'failed_scrape' clobber,
  -- while still allowing a re-scrape with more items to overwrite.
  AND NOT (status = 'scraped' AND $2 <> 'scraped');
