package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"fodmap/data/schemas"

	"github.com/google/uuid"
)

func TestCheckpointing(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.checkpoint")

	// Read missing file returns 0
	n, err := readCheckpoint(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}

	// Write 42
	if err := writeCheckpoint(path, 42); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	// Read back 42
	n, err = readCheckpoint(path)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if n != 42 {
		t.Errorf("expected 42, got %d", n)
	}
}

func TestReadCheckpoint_Corrupt(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "corrupt.checkpoint")
	if err := os.WriteFile(path, []byte("not-a-number"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	_, err := readCheckpoint(path)
	if err == nil {
		t.Errorf("expected parse error")
	}
}

// TestBuildYelpUUIDMap_Integration exercises buildYelpUUIDMap against a live
// Postgres: upserts Yelp businesses, verifies the returned map maps Yelp string
// IDs to valid UUIDs, and confirms a second call is idempotent. Skipped unless
// POSTGRES_DSN is set.
func TestBuildYelpUUIDMap_Integration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set; skipping buildYelpUUIDMap integration test")
	}

	businessMap := map[string]schemas.Business{
		"yelp-1": {BusinessID: "yelp-1", Name: "Test Cafe"},
		"yelp-2": {BusinessID: "yelp-2", Name: "Test Bistro"},
	}

	ctx := context.Background()
	m, err := buildYelpUUIDMap(ctx, dsn, businessMap)
	if err != nil {
		t.Fatalf("buildYelpUUIDMap: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(m))
	}
	for yelpID, uid := range m {
		if uid == (uuid.UUID{}) {
			t.Errorf("yelp_id %q mapped to zero UUID", yelpID)
		}
	}

	// Idempotent: a second call returns the same UUIDs (ON CONFLICT DO UPDATE).
	m2, err := buildYelpUUIDMap(ctx, dsn, businessMap)
	if err != nil {
		t.Fatalf("second buildYelpUUIDMap: %v", err)
	}
	for yelpID, uid := range m {
		if m2[yelpID] != uid {
			t.Errorf("yelp_id %q not idempotent: first=%s second=%s", yelpID, uid, m2[yelpID])
		}
	}
}
