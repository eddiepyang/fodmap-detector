// Package cli provides helper functions for constructing River clients and
// migrators that target a configurable Postgres schema (default "river").
//
// River v0.39 supports a Schema field on both rivermigrate.Config and
// river.Config; setting it routes all of River's internal table creation
// and reads (river_job, river_leader, river_queue, river_client,
// river_migration) to that schema. The schema must pre-exist — these helpers
// CREATE SCHEMA IF NOT EXISTS before migrating.
//
// All River client construction in the CLI goes through newRiverClient /
// newRiverMigrator so a single --river-schema flag governs every site.
package cli

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// riverSchemaName returns the configured River schema, defaulting to "river".
// Set via --river-schema flag or RIVER_SCHEMA env (bound in rootCmd init).
func riverSchemaName() string {
	s := viper.GetString("river-schema")
	if s == "" {
		return "river"
	}
	return s
}

// newRiverMigrator builds a River migrator targeting the configured schema.
// The caller is responsible for CREATE SCHEMA IF NOT EXISTS (see
// ensureRiverSchema) before calling Migrate.
func newRiverMigrator(pool *pgxpool.Pool) (*rivermigrate.Migrator[pgx.Tx], error) {
	return rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{
		Schema: riverSchemaName(),
	})
}

// newRiverClient builds a River client targeting the configured schema. Use
// for all one-shot Insert sites and the long-running pipeline. cfg may be
// nil (a fresh river.Config is used); when non-nil its fields are preserved
// and only Schema is overridden.
func newRiverClient(pool *pgxpool.Pool, cfg *river.Config) (*river.Client[pgx.Tx], error) {
	if cfg == nil {
		cfg = &river.Config{}
	}
	cfg.Schema = riverSchemaName()
	return river.NewClient(riverpgxv5.New(pool), cfg)
}

// ensureRiverSchema creates the River schema if it does not exist. River's
// migrator creates tables but not the schema itself, so this must run before
// Migrate. safeSchema is the validated/quoted schema name.
func ensureRiverSchema(ctx context.Context, pool *pgxpool.Pool) error {
	schema := riverSchemaName()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+quoteIdent(schema)); err != nil {
		return fmt.Errorf("create river schema %q: %w", schema, err)
	}
	return nil
}

// quoteIdent wraps an SQL identifier in double quotes, escaping embedded
// double quotes. Used for schema names in DDL that pgx doesn't parameterize.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// detectExistingRiverDeployment checks whether an existing deployment has
// river_job in public but not in the configured river schema. Returns an
// error describing the one-time migration step when the half-migrated state
// is detected, so existing deployments don't silently break.
//
// Run after domain migrations, before ensureRiverSchema + River migrate.
// sqldb is the database/sql handle (separate from the pgxpool used for River
// migrations) so the check runs in the same transaction style as the domain
// migrations.
func detectExistingRiverDeployment(ctx context.Context, sqldb *sql.DB) error {
	schema := riverSchemaName()
	row := sqldb.QueryRowContext(ctx, `
		SELECT
		  EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = 'public'  AND tablename = 'river_job'),
		  EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = $1       AND tablename = 'river_job')
	`, schema)
	var publicHas, riverHas bool
	if err := row.Scan(&publicHas, &riverHas); err != nil {
		return fmt.Errorf("checking river schema state: %w", err)
	}
	if publicHas && !riverHas {
		slog.Error("existing deployment detected: river_job is in public schema but not in the configured river schema",
			"schema", schema)
		slog.Error("run the one-time migration then re-run migrate-up:",
			"step1", "ALTER TABLE public.river_job SET SCHEMA "+schema+";",
			"step2", "ALTER TABLE public.river_leader SET SCHEMA "+schema+";",
			"step3", "ALTER TABLE public.river_queue SET SCHEMA "+schema+";",
			"step4", "ALTER TABLE public.river_client SET SCHEMA "+schema+";",
			"step5", "ALTER TABLE public.river_migration SET SCHEMA "+schema+";",
			"note", "omit any table that does not exist; or DROP TABLE ... CASCADE and re-migrate (loses queued jobs)")
		return fmt.Errorf("river_job exists in public schema but not in %q: run the one-time ALTER TABLE ... SET SCHEMA migration then re-run migrate-up (see docs/plans/river-schema-and-dual-write-plan.md)",
			schema)
	}
	return nil
}

// addRiverSchemaFlag registers the --river-schema persistent flag and binds
// it to viper + the RIVER_SCHEMA env var. Call from rootCmd init.
func addRiverSchemaFlag(rootCmd *cobra.Command) {
	rootCmd.PersistentFlags().String("river-schema", "river",
		"Postgres schema for River's internal tables (river_job, river_leader, ...)")
	_ = viper.BindPFlag("river-schema", rootCmd.PersistentFlags().Lookup("river-schema"))
	_ = viper.BindEnv("river-schema", "RIVER_SCHEMA")
}
