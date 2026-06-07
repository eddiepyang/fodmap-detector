WITH best_chunks AS (
    -- DISTINCT ON keeps only the closest chunk per review; the outer ORDER BY
    -- then re-sorts by descending certainty for the final result set.
    SELECT DISTINCT ON (r.review_id)
        r.review_id,
        r.business_id,
        r.business_name,
        r.city,
        r.state,
        r.text,
        rc.chunk_text,
        (1 - (rc.embedding <=> $1)) AS certainty,
        (rc.embedding <=> $1)       AS distance
    FROM  review_chunks rc
    JOIN  reviews r ON rc.review_id = r.review_id
    {{.Where}}
    ORDER BY r.review_id, distance ASC
)
SELECT review_id, business_id, business_name, city, state, text, chunk_text, certainty
FROM   best_chunks
ORDER  BY certainty DESC
LIMIT  {{.LimitArg}}
