INSERT INTO restaurants (yelp_id, dba, status)
VALUES ($1, $2, 'scraped')
ON CONFLICT (yelp_id) DO UPDATE SET
    dba = EXCLUDED.dba
RETURNING id;