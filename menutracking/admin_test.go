package menutracking

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	fodmapdb "fodmap/internal/db"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// openTestPool returns a pgxpool connected to a freshly-created temp database.
// It uses the local docker-compose Postgres instance. Tests that require it are
// skipped if POSTGRES_DSN_TEST is not set.
func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN_TEST")
	if dsn == "" {
		dsn = "postgres://fodmap:fodmap@127.0.0.1:5432/fodmap"
	}

	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin db: %v", err)
	}
	defer func() { _ = adminDB.Close() }()

	if err := adminDB.Ping(); err != nil {
		t.Skipf("cannot reach test postgres (%v); skipping", err)
	}

	tmpName := fmt.Sprintf("menutracking_test_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE " + tmpName); err != nil {
		t.Skipf("cannot create temp database (%v); skipping", err)
	}

	var pool *pgxpool.Pool

	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		// adminDB was closed by the defer above; re-open for cleanup.
		cleanupDB, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Logf("failed to open admin db for cleanup: %v", err)
			return
		}
		defer func() { _ = cleanupDB.Close() }()
		if _, err := cleanupDB.Exec("DROP DATABASE IF EXISTS " + tmpName + " WITH (FORCE)"); err != nil {
			t.Logf("failed to drop temp database %s: %v", tmpName, err)
		}
	})

	tmpDSN, err := swapDSNDBName(dsn, tmpName)
	if err != nil {
		t.Fatalf("derive temp dsn: %v", err)
	}

	sqlDB, err := sql.Open("pgx", tmpDSN)
	if err != nil {
		t.Fatalf("open temp sql db: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := fodmapdb.MigrateUp(sqlDB); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	pool, err = pgxpool.New(context.Background(), tmpDSN)
	if err != nil {
		t.Fatalf("open temp pool: %v", err)
	}
	return pool
}

// swapDSNDBName replaces the database name in a URL-style postgres DSN.
func swapDSNDBName(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parsing dsn: %w", err)
	}
	if !strings.HasPrefix(u.Scheme, "postgres") {
		return "", fmt.Errorf("unsupported dsn scheme %q", u.Scheme)
	}
	u.Path = "/" + name
	return u.String(), nil
}

func TestAdminHandler_ListSources(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	if err := InsertSource(context.Background(), pool, &Source{
		Name:         "EPA",
		URL:          "https://epa.gov/regulations",
		Domain:       "epa.gov",
		Tier:         "gov",
		CronSchedule: "@weekly",
		MaxTokens:    32000,
	}); err != nil {
		t.Fatalf("InsertSource: %v", err)
	}

	h := &MenutrackingAdminHandler{Pool: pool}
	req := httptest.NewRequest(http.MethodGet, "/menutracking/sources", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var sources []Source
	if err := json.Unmarshal(rec.Body.Bytes(), &sources); err != nil {
		t.Fatalf("unmarshal sources: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	if sources[0].Domain != "epa.gov" {
		t.Errorf("expected domain epa.gov, got %q", sources[0].Domain)
	}
}

func TestAdminHandler_ListSources_MethodNotAllowed(t *testing.T) {
	h := &MenutrackingAdminHandler{}
	req := httptest.NewRequest(http.MethodPost, "/menutracking/sources", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestAdminHandler_ListDiscardedJobs_Empty(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	// river_job is managed by river migrate-up, not the domain migrations.
	// Create the minimal table shape so listDiscardedJobs can run.
	if _, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS river_job (
			id bigserial PRIMARY KEY,
			kind text NOT NULL,
			args jsonb,
			state text NOT NULL DEFAULT 'available',
			final_attempt_at timestamptz,
			created_at timestamptz NOT NULL DEFAULT now()
		)
	`); err != nil {
		t.Fatalf("create river_job stub: %v", err)
	}

	h := &MenutrackingAdminHandler{Pool: pool}
	req := httptest.NewRequest(http.MethodGet, "/menutracking/jobs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var jobs []DiscardedJob
	if err := json.Unmarshal(rec.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("unmarshal jobs: %v", err)
	}
	if jobs == nil {
		t.Error("expected empty slice, got nil")
	}
}

func TestAdminHandler_ReloadSources(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	ch := make(chan struct{}, 1)
	h := &MenutrackingAdminHandler{Pool: pool, ReloadSignal: ch}
	req := httptest.NewRequest(http.MethodPost, "/menutracking/reload", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	select {
	case <-ch:
		// expected
	case <-time.After(time.Second):
		t.Error("expected signal on ReloadSignal")
	}
}

func TestAdminHandler_ReloadSources_MethodNotAllowed(t *testing.T) {
	h := &MenutrackingAdminHandler{}
	req := httptest.NewRequest(http.MethodGet, "/menutracking/reload", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestAdminHandler_NotFound(t *testing.T) {
	h := &MenutrackingAdminHandler{}
	req := httptest.NewRequest(http.MethodGet, "/menutracking/unknown", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestDeadLetterHandler(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	if err := DeadLetterHandler(context.Background(), pool, "menutracking.scrape", []byte(`{"url":"https://epa.gov"}`), "boom"); err != nil {
		t.Fatalf("DeadLetterHandler: %v", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM menutracking_dead_letter").Scan(&count); err != nil {
		t.Fatalf("count dead letters: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 dead letter, got %d", count)
	}
}
