// Package store manages the database SQL queries for the menutracking
// domain tables. Schema creation is handled by the centralised migration
// runner (internal/db); River's own tables (river_job, river_leader, etc.)
// are managed separately by "river migrate-up".
package store

import _ "embed"

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
