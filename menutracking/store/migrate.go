// Package store manages the database SQL queries for the menutracking
// domain tables. Schema creation is handled by the centralised migration
// runner (internal/db); River's own tables (river_job, river_leader, etc.)
// are managed separately by "river migrate-up".
package store

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"text/template"
)

// riverSchema is the Postgres schema where River's tables live. Defaults to
// "river"; overridden by the CLI via SetRiverSchema at startup so the
// discarded-jobs admin query reads from the correct schema.
var riverSchema = "river"

// SetRiverSchema configures the schema used for River's internal tables
// (river_job, river_leader, ...) in SQL emitted by this package. The CLI
// calls this once at startup with the value of --river-schema.
func SetRiverSchema(s string) {
	if s = strings.TrimSpace(s); s != "" {
		riverSchema = s
	}
}

// RiverSchema returns the currently configured River schema.
func RiverSchema() string { return riverSchema }

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

// listDiscardedJobsSQLRaw is the templated SQL for listing discarded river
// jobs. Render via RenderListDiscardedJobsSQL() to inject the River schema.
//
//go:embed sql/list_discarded_jobs.sql
var listDiscardedJobsSQLRaw string

var listDiscardedJobsTmpl = template.Must(template.New("list_discarded_jobs").Parse(listDiscardedJobsSQLRaw))

// RenderListDiscardedJobsSQL returns the discarded-jobs query with the
// current River schema substituted. Callers should use this (not the raw
// embedded string) so the query reads from the configured schema.
func RenderListDiscardedJobsSQL() string {
	var buf bytes.Buffer
	if err := listDiscardedJobsTmpl.Execute(&buf, struct{ Schema string }{Schema: quoteIdent(riverSchema)}); err != nil {
		// template.Execute only errors on missing field/IO; both impossible here.
		return fmt.Sprintf("SELECT 'template error: %v'::text", err)
	}
	return buf.String()
}

// ListDiscardedJobsSQL lists discarded river jobs for admin display. It is
// the rendered form of the embedded template (see RenderListDiscardedJobsSQL)
// and is initialized to the default schema; callers that change the schema
// at runtime must call RenderListDiscardedJobsSQL() after SetRiverSchema.
//
// Prefer RenderListDiscardedJobsSQL() in new code; this var is kept for
// backward compatibility with existing call sites that read it directly.
var ListDiscardedJobsSQL = RenderListDiscardedJobsSQL()

// quoteIdent wraps an SQL identifier in double quotes, escaping embedded
// double quotes. Used for schema names in templated SQL.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// InsertDeadLetterSQL persists a discarded river job to the audit table.
//
//go:embed sql/insert_dead_letter.sql
var InsertDeadLetterSQL string
