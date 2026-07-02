-- Bucket free-form last_error strings into a coarse failure taxonomy: keep the
-- text before the first ':' (the Go error-wrap stage prefix, e.g. "extract
-- menu"), then collapse digit runs so per-restaurant details such as
-- "no URL found for 50044186 (attempt 3/3)" merge into one bucket.
SELECT substring(regexp_replace(split_part(last_error, ':', 1), '[0-9]+', 'N', 'g') for 80) AS reason,
       count(*)::int
FROM restaurants
WHERE status = 'failed_scrape' AND last_error IS NOT NULL AND last_error <> ''
GROUP BY 1
ORDER BY count(*) DESC
LIMIT 10;
