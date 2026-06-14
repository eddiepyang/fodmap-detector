// Package db provides the centralised database migration runner. All DDL
// lives in versioned .sql files under the top-level migrations/ directory,
// embedded into the binary via //go:embed, and applied by golang-migrate.
// River's own tables (river_job, river_leader, etc.) are NOT managed here —
// they are handled separately by "river migrate-up".
package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// MigrateUp runs all pending up-migrations against the given database.
// It is safe to call on a fresh database (all migrations run) and on an
// existing database that is already at the latest version (no-op).
// The caller owns db and is responsible for closing it; MigrateUp never does.
func MigrateUp(db *sql.DB) error {
	m, src, err := newMigrate(db)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// MigrateDown rolls back one migration step. The caller owns db.
func MigrateDown(db *sql.DB) error {
	m, src, err := newMigrate(db)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	if err := m.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("rollback migration: %w", err)
	}
	return nil
}

// ForceVersion sets the migration version without executing any statements.
// This is used to mark the baseline as already-applied on existing databases
// that were created before golang-migrate was adopted. The caller owns db.
func ForceVersion(db *sql.DB, version int) error {
	m, src, err := newMigrate(db)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	if err := m.Force(version); err != nil {
		return fmt.Errorf("force version %d: %w", version, err)
	}
	return nil
}

// Version returns the current migration version and dirty state.
// The caller owns db.
func Version(db *sql.DB) (uint, bool, error) {
	m, src, err := newMigrate(db)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = src.Close() }()

	v, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, fmt.Errorf("read version: %w", err)
	}
	return v, dirty, nil
}

// newMigrate builds a migrate.Migrate over the caller's *sql.DB and the
// embedded migration source. It returns the source as an io.Closer so callers
// can release it WITHOUT calling m.Close(), which would close the caller's db
// (golang-migrate's postgres driver closes the underlying *sql.DB on Close).
func newMigrate(db *sql.DB) (*migrate.Migrate, io.Closer, error) {
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return nil, nil, fmt.Errorf("create migrate driver: %w", err)
	}

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return nil, nil, fmt.Errorf("create migration source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		return nil, nil, fmt.Errorf("create migrate instance: %w", err)
	}
	return m, src, nil
}
