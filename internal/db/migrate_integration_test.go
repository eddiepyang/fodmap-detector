package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestMigrate_Integration exercises the real baseline SQL against a live
// Postgres instance: it runs the full up -> idempotent up -> down -> up cycle
// and asserts every baseline table is created. It is skipped unless
// POSTGRES_DSN is set, so the default "go test ./..." / "make check" run stays
// green without a database.
//
// To avoid mutating the schema of the database named in POSTGRES_DSN, the test
// creates a throwaway database, runs all migrations there, and drops it when
// finished. The DSN's user must be able to CREATE DATABASE; if not (or the
// server is unreachable), the test skips rather than fails.
func TestMigrate_Integration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set; skipping migration integration test")
	}

	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin db: %v", err)
	}
	defer func() { _ = adminDB.Close() }()

	if err := adminDB.Ping(); err != nil {
		t.Skipf("cannot reach POSTGRES_DSN (%v); skipping", err)
	}

	tmpName := fmt.Sprintf("migrate_it_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE " + tmpName); err != nil {
		t.Skipf("cannot create temp database (%v); skipping", err)
	}
	// Deferred LIFO order matters: the temp-db connection (closed below) must be
	// released before the DROP DATABASE runs, and adminDB must still be open for
	// the drop — hence defers rather than t.Cleanup.
	defer func() {
		if _, err := adminDB.Exec("DROP DATABASE IF EXISTS " + tmpName + " WITH (FORCE)"); err != nil {
			t.Logf("failed to drop temp database %s: %v", tmpName, err)
		}
	}()

	tmpDSN, err := swapDBName(dsn, tmpName)
	if err != nil {
		t.Fatalf("derive temp dsn: %v", err)
	}

	db, err := sql.Open("pgx", tmpDSN)
	if err != nil {
		t.Fatalf("open temp db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Up: apply all migrations (vector extension + baseline).
	if err := MigrateUp(db); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	v, dirty, err := Version(db)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if dirty {
		t.Fatalf("migration state dirty at version %d", v)
	}
	if v == 0 {
		t.Fatalf("expected version > 0 after up, got %d", v)
	}

	// Every table should now exist.
	wantTables := []string{
		"users", "user_profiles", "conversations", "messages",
		"reviews", "review_chunks", "fodmap_ingredients",
		"fodmap_catalog", "fodmap_meta",
		"sources", "extraction_rules", "regulatory_updates", "menutracking_dead_letter",
		"restaurants", "menu_items",
	}
	for _, tbl := range wantTables {
		if !tableExists(t, db, tbl) {
			t.Errorf("expected table %q to exist after MigrateUp", tbl)
		}
	}

	// Idempotency: a second Up is a no-op (ErrNoChange is handled).
	if err := MigrateUp(db); err != nil {
		t.Fatalf("second MigrateUp should be a no-op: %v", err)
	}

	// Down: roll back all steps one by one to ensure every down migration works cleanly.
	for i := uint(0); i < v; i++ {
		if err := MigrateDown(db); err != nil {
			t.Fatalf("MigrateDown at step %d (reverting to version %d): %v", i+1, v-1-i, err)
		}
	}
	for _, tbl := range wantTables {
		if tableExists(t, db, tbl) {
			t.Errorf("expected table %q to be dropped after full rollback", tbl)
		}
	}

	// Up again: the full cycle re-creates everything cleanly.
	if err := MigrateUp(db); err != nil {
		t.Fatalf("MigrateUp after down: %v", err)
	}
	if !tableExists(t, db, "fodmap_catalog") {
		t.Error("expected fodmap_catalog to exist after re-running MigrateUp")
	}
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRow(
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`,
		name,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("checking table %q: %v", name, err)
	}
	return exists
}

// swapDBName returns dsn with its database name replaced by name. It supports
// URL-style DSNs (e.g. "postgres://user:pass@host:port/dbname?sslmode=disable").
func swapDBName(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parsing dsn: %w", err)
	}
	if !strings.HasPrefix(u.Scheme, "postgres") {
		return "", fmt.Errorf("unsupported dsn scheme %q (need a URL-style postgres DSN)", u.Scheme)
	}
	u.Path = "/" + name
	return u.String(), nil
}
