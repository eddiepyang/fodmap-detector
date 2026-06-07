// Package store manages the database schema and SQL queries for the menutracking
// domain tables. River's own tables (river_job, river_leader, etc.) are managed
// by "river migrate-up" and live in the 'river' schema, not here.
package store

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

// MigrateUp creates the menutracking domain tables (sources, extraction_rules,
// regulatory_updates, menutracking_dead_letter) in the public schema. It is
// idempotent — all statements use IF NOT EXISTS.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, schemaSQL)
	if err != nil {
		return err
	}
	return nil
}

//go:embed sql/list_sources.sql
var ListSourcesSQL string

//go:embed sql/get_source_by_id.sql
var GetSourceByIDSQL string

//go:embed sql/insert_source.sql
var InsertSourceSQL string

//go:embed sql/get_active_rule.sql
var GetActiveRuleSQL string

//go:embed sql/insert_proposed_rule.sql
var InsertProposedRuleSQL string

//go:embed sql/promote_rule.sql
var PromoteRuleSQL string

//go:embed sql/reject_rule.sql
var RejectRuleSQL string

//go:embed sql/upsert_regulatory_update.sql
var UpsertRegulatoryUpdateSQL string

//go:embed sql/get_proposed_rule.sql
var GetProposedRuleSQL string

//go:embed sql/list_discarded_jobs.sql
var ListDiscardedJobsSQL string