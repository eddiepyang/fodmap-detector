-- name: list-discarded-jobs
-- List discarded river jobs, ordered by most recent first.
-- {{.Schema}} is the River schema (default "river"), injected by
-- menutracking/store.RenderListDiscardedJobsSQL at load time.
SELECT kind, args, final_attempt_at, state, created_at FROM {{.Schema}}.river_job
WHERE state = 'discarded'
ORDER BY final_attempt_at DESC NULLS LAST
LIMIT $1;