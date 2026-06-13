package db

import (
	"database/sql"
	"testing"

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
