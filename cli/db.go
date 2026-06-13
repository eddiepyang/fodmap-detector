package cli

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"fodmap/internal/db"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Database management commands.",
}

var dbMigrateUpCmd = &cobra.Command{
	Use:   "migrate-up",
	Short: "Run all pending database migrations (domain tables + river schema).",
	RunE:  runDBMigrateUp,
}

var dbMigrateDownCmd = &cobra.Command{
	Use:   "migrate-down",
	Short: "Roll back one database migration step.",
	RunE:  runDBMigrateDown,
}

var dbMigrateForceCmd = &cobra.Command{
	Use:   "migrate-force <version>",
	Short: "Force-set the migration version without executing statements (for existing databases).",
	Args:  cobra.ExactArgs(1),
	RunE:  runDBMigrateForce,
}

var dbMigrateVersionCmd = &cobra.Command{
	Use:   "migrate-version",
	Short: "Print the current migration version.",
	RunE:  runDBMigrateVersion,
}

func init() {
	rootCmd.AddCommand(dbCmd)
	dbCmd.AddCommand(dbMigrateUpCmd)
	dbCmd.AddCommand(dbMigrateDownCmd)
	dbCmd.AddCommand(dbMigrateForceCmd)
	dbCmd.AddCommand(dbMigrateVersionCmd)
}

func openDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return db, nil
}

func runDBMigrateUp(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	dsn := viper.GetString("postgres-dsn")
	if dsn == "" {
		return fmt.Errorf("postgres-dsn is required (set via --postgres-dsn or POSTGRES_DSN env)")
	}

	sqldb, err := openDB(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = sqldb.Close() }()

	slog.Info("running domain migrations")
	if err := db.MigrateUp(sqldb); err != nil {
		return fmt.Errorf("domain migrations: %w", err)
	}
	slog.Info("domain migrations complete")

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connecting to postgres for river: %w", err)
	}
	defer pool.Close()

	slog.Info("running river migrations")
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{})
	if err != nil {
		return fmt.Errorf("creating river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("running river migrate-up: %w", err)
	}
	slog.Info("river migrations complete")

	return nil
}

func runDBMigrateDown(cmd *cobra.Command, args []string) error {
	dsn := viper.GetString("postgres-dsn")
	if dsn == "" {
		return fmt.Errorf("postgres-dsn is required (set via --postgres-dsn or POSTGRES_DSN env)")
	}

	sqldb, err := openDB(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = sqldb.Close() }()

	if err := db.MigrateDown(sqldb); err != nil {
		return fmt.Errorf("rollback: %w", err)
	}
	slog.Info("rolled back one migration step")
	return nil
}

func runDBMigrateForce(cmd *cobra.Command, args []string) error {
	var version int
	if _, err := fmt.Sscanf(args[0], "%d", &version); err != nil {
		return fmt.Errorf("invalid version %q: must be a non-negative integer", args[0])
	}

	dsn := viper.GetString("postgres-dsn")
	if dsn == "" {
		return fmt.Errorf("postgres-dsn is required (set via --postgres-dsn or POSTGRES_DSN env)")
	}

	sqldb, err := openDB(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = sqldb.Close() }()

	if err := db.ForceVersion(sqldb, version); err != nil {
		return fmt.Errorf("force version: %w", err)
	}
	slog.Info("forced migration version", "version", version)
	return nil
}

func runDBMigrateVersion(cmd *cobra.Command, args []string) error {
	dsn := viper.GetString("postgres-dsn")
	if dsn == "" {
		return fmt.Errorf("postgres-dsn is required (set via --postgres-dsn or POSTGRES_DSN env)")
	}

	sqldb, err := openDB(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = sqldb.Close() }()

	v, dirty, err := db.Version(sqldb)
	if err != nil {
		return fmt.Errorf("read version: %w", err)
	}
	if dirty {
		slog.Warn("migration state is dirty", "version", v)
	}
	fmt.Printf("version: %d\n", v)
	return nil
}
