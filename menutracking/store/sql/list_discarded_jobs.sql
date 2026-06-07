-- name: list-discarded-jobs
-- List discarded river jobs, ordered by most recent first.
SELECT kind, args, final_attempt_at, state, created_at FROM river_job
WHERE state = 'discarded'
ORDER BY final_attempt_at DESC NULLS LAST
LIMIT $1;