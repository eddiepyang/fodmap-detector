package server

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"fodmap/search"
)

// menuStoreStub is a recording MenuStore stub for dual-store tests.
type menuStoreStub struct {
	ensureErr   error
	upsertErr   error
	searchErr   error
	upserted    [][]search.MenuItem
	ensureCalls int
	searchCalls int
}

func (s *menuStoreStub) EnsureMenuSchema(_ context.Context) error {
	s.ensureCalls++
	return s.ensureErr
}

func (s *menuStoreStub) BatchUpsertMenu(_ context.Context, items []search.MenuItem) error {
	s.upserted = append(s.upserted, items)
	return s.upsertErr
}

func (s *menuStoreStub) SearchMenu(_ context.Context, _ string, _ int) ([]search.MenuItem, error) {
	s.searchCalls++
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	return []search.MenuItem{{DishName: "from-primary"}}, nil
}

func (s *menuStoreStub) ListMenuItems(_ context.Context, _ string, _, _ int) ([]search.MenuItem, int, error) {
	return nil, 0, nil
}

func TestDualMenuStore_PrimaryOK_SecondaryOK(t *testing.T) {
	primary := &menuStoreStub{}
	secondary := &menuStoreStub{}
	d := NewDualMenuStore(primary, secondary)

	items := []search.MenuItem{{DishName: "a"}, {DishName: "b"}}
	if err := d.BatchUpsertMenu(context.Background(), items); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(primary.upserted) != 1 || len(primary.upserted[0]) != 2 {
		t.Errorf("primary got %d batches, %d items", len(primary.upserted), len(primary.upserted[0]))
	}
	if len(secondary.upserted) != 1 {
		t.Errorf("secondary should have 1 batch, got %d", len(secondary.upserted))
	}
}

func TestDualMenuStore_PrimaryOK_SecondaryFail_NoError(t *testing.T) {
	primary := &menuStoreStub{}
	secondary := &menuStoreStub{upsertErr: errors.New("weaviate down")}
	d := NewDualMenuStore(primary, secondary)

	// Capture the warn log so it doesn't pollute test output.
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	slog.SetDefault(logger)

	items := []search.MenuItem{{DishName: "a"}}
	if err := d.BatchUpsertMenu(context.Background(), items); err != nil {
		t.Fatalf("secondary failure should not propagate; got %v", err)
	}
	if len(primary.upserted) != 1 {
		t.Errorf("primary should have received the batch")
	}
	if !strings.Contains(buf.String(), "secondary upsert failed") {
		t.Errorf("expected warn log about secondary failure, got: %s", buf.String())
	}
}

func TestDualMenuStore_PrimaryFail_Error(t *testing.T) {
	primary := &menuStoreStub{upsertErr: errors.New("postgres down")}
	secondary := &menuStoreStub{}
	d := NewDualMenuStore(primary, secondary)

	err := d.BatchUpsertMenu(context.Background(), []search.MenuItem{{DishName: "a"}})
	if err == nil {
		t.Fatal("primary failure must propagate")
	}
	if !strings.Contains(err.Error(), "primary upsert") {
		t.Errorf("expected 'primary upsert' in error, got %v", err)
	}
	if len(secondary.upserted) != 0 {
		t.Errorf("secondary should not be called when primary fails")
	}
}

func TestDualMenuStore_SecondaryNil_Passthrough(t *testing.T) {
	primary := &menuStoreStub{}
	d := NewDualMenuStore(primary, nil)

	if err := d.EnsureMenuSchema(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if primary.ensureCalls != 1 {
		t.Errorf("primary EnsureMenuSchema should be called once, got %d", primary.ensureCalls)
	}
	if err := d.BatchUpsertMenu(context.Background(), []search.MenuItem{{}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	res, err := d.SearchMenu(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) != 1 || res[0].DishName != "from-primary" {
		t.Errorf("search should read primary, got %v", res)
	}
	if primary.searchCalls != 1 {
		t.Errorf("primary SearchMenu should be called once, got %d", primary.searchCalls)
	}
}

func TestDualMenuStore_SearchReadsPrimaryOnly(t *testing.T) {
	primary := &menuStoreStub{}
	secondary := &menuStoreStub{}
	d := NewDualMenuStore(primary, secondary)

	_, _ = d.SearchMenu(context.Background(), "q", 5)
	if secondary.searchCalls != 0 {
		t.Errorf("secondary should never be read; got %d calls", secondary.searchCalls)
	}
	if primary.searchCalls != 1 {
		t.Errorf("primary should be read once, got %d", primary.searchCalls)
	}
}

func TestDualMenuStore_EnsureMenuSchema_PrimaryFail(t *testing.T) {
	primary := &menuStoreStub{ensureErr: errors.New("primary schema err")}
	secondary := &menuStoreStub{}
	d := NewDualMenuStore(primary, secondary)

	err := d.EnsureMenuSchema(context.Background())
	if err == nil {
		t.Fatal("primary EnsureMenuSchema failure must propagate")
	}
	if secondary.ensureCalls != 0 {
		t.Errorf("secondary EnsureMenuSchema should not run when primary fails")
	}
}

func TestNewMenuStore_UnknownType(t *testing.T) {
	_, err := NewMenuStore(context.Background(), MenuStoreConfig{Type: "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestNewMenuStore_Postgres_MissingDSN(t *testing.T) {
	_, err := NewMenuStore(context.Background(), MenuStoreConfig{Type: "postgres"})
	if err == nil {
		t.Fatal("expected error for missing DSN")
	}
}

func TestNewMenuStore_Weaviate_MissingHost(t *testing.T) {
	_, err := NewMenuStore(context.Background(), MenuStoreConfig{Type: "weaviate"})
	if err == nil {
		t.Fatal("expected error for missing weaviate host")
	}
}

func TestNewMenuStore_Dual_MissingPostgresDSN(t *testing.T) {
	_, err := NewMenuStore(context.Background(), MenuStoreConfig{
		Type:         "dual",
		WeaviateHost: "localhost:8090",
	})
	if err == nil {
		t.Fatal("expected error for missing postgres DSN in dual mode")
	}
}

func TestNewMenuStore_Dual_MissingWeaviateHost(t *testing.T) {
	_, err := NewMenuStore(context.Background(), MenuStoreConfig{
		Type:        "dual",
		PostgresDSN: "postgres://u:p@localhost/db",
	})
	if err == nil {
		t.Fatal("expected error for missing weaviate host in dual mode")
	}
}
