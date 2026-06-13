package db

import (
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestMigrateDown_InvalidDSN(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://invalid:invalid@localhost:0/invalid")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := MigrateDown(db); err == nil {
		t.Error("expected error for invalid DSN")
	}
}

func TestForceVersion_InvalidDSN(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://invalid:invalid@localhost:0/invalid")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := ForceVersion(db, 1); err == nil {
		t.Error("expected error for invalid DSN")
	}
}

func TestSwapDBName(t *testing.T) {
	tests := []struct {
		name    string
		dsn     string
		newName string
		want    string
		wantErr bool
	}{
		{
			name:    "url style with params",
			dsn:     "postgres://user:pass@host:5432/olddb?sslmode=disable",
			newName: "newdb",
			want:    "postgres://user:pass@host:5432/newdb?sslmode=disable",
		},
		{
			name:    "postgresql scheme",
			dsn:     "postgresql://user@host:5432/olddb",
			newName: "tmp",
			want:    "postgresql://user@host:5432/tmp",
		},
		{
			name:    "non-postgres scheme",
			dsn:     "mysql://user@host/db",
			newName: "x",
			wantErr: true,
		},
		{
			name:    "unparseable dsn",
			dsn:     "://bad",
			newName: "x",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := swapDBName(tt.dsn, tt.newName)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("swapDBName(%q): expected error, got nil", tt.dsn)
				}
				return
			}
			if err != nil {
				t.Fatalf("swapDBName(%q): unexpected error: %v", tt.dsn, err)
			}
			if got != tt.want {
				t.Errorf("swapDBName(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}
