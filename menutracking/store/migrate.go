// Package store manages the database SQL queries for the menutracking
// domain tables. Schema creation is handled by the centralised migration
// runner (internal/db); River's own tables (river_job, river_leader, etc.)
// are managed separately by "river migrate-up".
package store

import _ "embed"

// ListSourcesSQL lists all configured scraping sources.
//
//go:embed sql/list_sources.sql
var ListSourcesSQL string

// SourceByIDSQL retrieves a source by its ID.
//
//go:embed sql/get_source_by_id.sql
var SourceByIDSQL string

// InsertSourceSQL inserts a new scraping source.
//
//go:embed sql/insert_source.sql
var InsertSourceSQL string

// ActiveRuleSQL retrieves the active extraction rule for a given domain.
//
//go:embed sql/get_active_rule.sql
var ActiveRuleSQL string

// InsertProposedRuleSQL writes a proposed extraction rule.
//
//go:embed sql/insert_proposed_rule.sql
var InsertProposedRuleSQL string

// PromoteRuleSQL promotes a proposed rule to active status.
//
//go:embed sql/promote_rule.sql
var PromoteRuleSQL string

// RejectRuleSQL rejects a proposed rule.
//
//go:embed sql/reject_rule.sql
var RejectRuleSQL string

// UpsertRegulatoryUpdateSQL upserts a regulatory update record.
//
//go:embed sql/upsert_regulatory_update.sql
var UpsertRegulatoryUpdateSQL string

// ProposedRuleSQL retrieves a proposed extraction rule by its ID.
//
//go:embed sql/get_proposed_rule.sql
var ProposedRuleSQL string

// ListDiscardedJobsSQL lists discarded river jobs for admin display.
//
//go:embed sql/list_discarded_jobs.sql
var ListDiscardedJobsSQL string

// InsertDeadLetterSQL persists a discarded river job to the audit table.
//
//go:embed sql/insert_dead_letter.sql
var InsertDeadLetterSQL string
