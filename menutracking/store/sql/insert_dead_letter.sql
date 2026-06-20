-- name: insert-dead-letter
-- Persist a discarded river job to the menutracking_dead_letter audit table.
INSERT INTO menutracking_dead_letter (job_kind, job_args, error)
VALUES ($1, $2, $3);