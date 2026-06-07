WITH chunk_scores AS (
    -- Score every chunk against the query vector, tagging each with its rank
    -- within its parent review so we can discard weaker chunks below.
    SELECT
        r.business_id,
        r.business_name,
        r.city,
        r.state,
        r.categories,
        r.stars,
        r.review_id,
        (1 - (rc.embedding <=> $1))                                    AS certainty,
        ROW_NUMBER() OVER (
            PARTITION BY r.review_id
            ORDER BY     rc.embedding <=> $1
        )                                                               AS chunk_rn
    FROM  review_chunks rc
    JOIN  reviews r ON rc.review_id = r.review_id
    {{.Where}}
),
top_reviews AS (
    -- Keep only the best-matching chunk per review, then rank reviews within
    -- each business so we can cap the sample size used for averaging.
    SELECT
        business_id,
        business_name,
        city,
        state,
        categories,
        stars,
        certainty,
        ROW_NUMBER() OVER (
            PARTITION BY business_id
            ORDER BY     certainty DESC
        ) AS rn
    FROM  chunk_scores
    WHERE chunk_rn = 1
),
avg_scores AS (
    -- Average the top-5 reviews per business to produce a stable score.
    SELECT
        business_id,
        MAX(business_name) AS name,
        MAX(city)          AS city,
        MAX(state)         AS state,
        MAX(categories)    AS categories,
        AVG(stars)         AS avg_stars,
        AVG(certainty)     AS avg_certainty
    FROM  top_reviews
    WHERE rn <= 5
    GROUP BY business_id
)
SELECT business_id, name, city, state, categories, avg_stars, avg_certainty
FROM   avg_scores
ORDER  BY avg_certainty DESC
LIMIT  {{.LimitArg}}
