package menutracking

import (
	"context"
	"testing"
)

func TestInsertAndListSources(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	src := &Source{
		Name:         "FDA",
		URL:          "https://fda.gov/food",
		Domain:       "fda.gov",
		Tier:         "gov",
		CronSchedule: "@daily",
		MaxTokens:    16000,
	}
	if err := InsertSource(ctx, pool, src); err != nil {
		t.Fatalf("InsertSource: %v", err)
	}
	if src.ID == "" {
		t.Fatal("expected ID assigned after insert")
	}
	if src.CreatedAt.IsZero() || src.UpdatedAt.IsZero() {
		t.Error("expected CreatedAt and UpdatedAt set")
	}

	sources, err := ListSources(ctx, pool)
	if err != nil {
		t.Fatalf("ListSources: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	if sources[0].Domain != "fda.gov" {
		t.Errorf("expected domain fda.gov, got %q", sources[0].Domain)
	}
}

func TestInsertSource_Idempotent(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	src := &Source{
		Name:         "FDA",
		URL:          "https://fda.gov/food",
		Domain:       "fda.gov",
		Tier:         "gov",
		CronSchedule: "@daily",
		MaxTokens:    16000,
	}
	if err := InsertSource(ctx, pool, src); err != nil {
		t.Fatalf("InsertSource first: %v", err)
	}
	firstID := src.ID

	// Re-inserting the same domain must update the existing row (upsert).
	src.ID = ""
	src.Name = "FDA Updated"
	if err := InsertSource(ctx, pool, src); err != nil {
		t.Fatalf("InsertSource second: %v", err)
	}
	if src.ID != firstID {
		t.Errorf("idempotent insert changed ID: %q vs %q", src.ID, firstID)
	}

	got, err := SourceByID(ctx, pool, firstID)
	if err != nil {
		t.Fatalf("SourceByID: %v", err)
	}
	if got.Name != "FDA Updated" {
		t.Errorf("expected updated name, got %q", got.Name)
	}
}

func TestSourceByID_NotFound(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	_, err := SourceByID(context.Background(), pool, "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("expected error for missing source")
	}
}
