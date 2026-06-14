package db

import (
	"database/sql"
	"io/fs"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestMigrateUp_InvalidDSN(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://invalid:invalid@localhost:0/invalid")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	err = MigrateUp(db)
	if err == nil {
		t.Error("expected error for invalid DSN")
	}
}

func TestVersion_InvalidDSN(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://invalid:invalid@localhost:0/invalid")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	_, _, err = Version(db)
	if err == nil {
		t.Error("expected error for invalid DSN")
	}
}

// TestEmbeddedMigrations validates that the embedded migrations load and form
// matched up/down pairs, without requiring a live database. This guards against
// a malformed filename or a missing .down.sql being shipped in the binary.
func TestEmbeddedMigrations(t *testing.T) {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}
	defer func() { _ = src.Close() }()

	first, err := src.First()
	if err != nil {
		t.Fatalf("no migrations found in embedded source: %v", err)
	}

	version := first
	count := 0
	for {
		if _, _, err := src.ReadUp(version); err != nil {
			t.Errorf("missing up migration for version %d: %v", version, err)
		}
		if _, _, err := src.ReadDown(version); err != nil {
			t.Errorf("missing down migration for version %d: %v", version, err)
		}
		count++

		next, err := src.Next(version)
		if err != nil {
			break // No more migrations.
		}
		version = next
	}

	if count == 0 {
		t.Fatal("expected at least one embedded migration")
	}

	// Every embedded file must follow the golang-migrate naming convention so
	// the iofs source recognises it.
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") && !strings.HasSuffix(name, ".down.sql") {
			t.Errorf("unexpected migration file name %q: must end in .up.sql or .down.sql", name)
		}
	}
}
