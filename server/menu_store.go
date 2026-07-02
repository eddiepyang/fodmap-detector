package server

import (
	"context"
	"fmt"
	"log/slog"

	"fodmap/search"
)

// DualMenuStore writes menu items to a primary MenuStore (the source of
// truth) and best-effort mirrors to an optional secondary. Reads always go
// to the primary. When secondary is nil the store behaves as a pure
// passthrough to primary.
//
// This enables a "dual-write" deployment: Postgres is the primary (and the
// only read path), and Weaviate is an optional mirror kept in sync
// best-effort so it can be exercised in local dev without coupling prod
// reads to it. A secondary write failure is logged and never blocks the
// scrape — the primary remains authoritative.
type DualMenuStore struct {
	primary   MenuStore
	secondary MenuStore // nil = primary-only
}

// NewDualMenuStore wraps primary as the source of truth and secondary as a
// best-effort mirror. secondary may be nil.
func NewDualMenuStore(primary, secondary MenuStore) *DualMenuStore {
	return &DualMenuStore{primary: primary, secondary: secondary}
}

// EnsureMenuSchema ensures both stores' schemas. A secondary failure is
// logged and does not block startup of the primary.
func (d *DualMenuStore) EnsureMenuSchema(ctx context.Context) error {
	if err := d.primary.EnsureMenuSchema(ctx); err != nil {
		return fmt.Errorf("primary ensure menu schema: %w", err)
	}
	if d.secondary != nil {
		if err := d.secondary.EnsureMenuSchema(ctx); err != nil {
			slog.Warn("dual menu store: secondary EnsureMenuSchema failed", "error", err)
		}
	}
	return nil
}

// BatchUpsertMenu writes to primary first (hard error on failure), then
// mirrors to secondary best-effort. A secondary failure is logged and never
// returned, so the scrape pipeline treats the primary write as authoritative.
func (d *DualMenuStore) BatchUpsertMenu(ctx context.Context, items []search.MenuItem) error {
	if err := d.primary.BatchUpsertMenu(ctx, items); err != nil {
		return fmt.Errorf("primary upsert: %w", err)
	}
	if d.secondary != nil {
		if err := d.secondary.BatchUpsertMenu(ctx, items); err != nil {
			slog.Warn("dual menu store: secondary upsert failed (primary succeeded)",
				"items", len(items), "error", err)
		}
	}
	return nil
}

// SearchMenu reads from the primary only. The secondary is write-only.
func (d *DualMenuStore) SearchMenu(ctx context.Context, query string, limit int) ([]search.MenuItem, error) {
	return d.primary.SearchMenu(ctx, query, limit)
}

// ListMenuItems reads from the primary only.
func (d *DualMenuStore) ListMenuItems(ctx context.Context, search string, limit, offset int) ([]search.MenuItem, int, error) {
	return d.primary.ListMenuItems(ctx, search, limit, offset)
}

// Compile-time check: DualMenuStore satisfies MenuStore.
var _ MenuStore = (*DualMenuStore)(nil)

// MenuStoreConfig selects a MenuStore backend.
//
// Type is one of "postgres" (default), "weaviate", or "dual". "dual" writes
// Postgres primary + Weaviate best-effort mirror and reads from Postgres
// only. Per-backend fields are required only for the selected type.
type MenuStoreConfig struct {
	Type           string // "postgres" | "weaviate" | "dual" (default "postgres")
	PostgresDSN    string
	WeaviateHost   string
	WeaviateScheme string
	WeaviateAPIKey string
	Embedder       search.Embedder
}

// NewMenuStore builds the configured MenuStore. For "weaviate" and "dual" it
// also runs EnsureMenuSchema on the Weaviate collection so the caller can
// skip the explicit init step.
//
// "dual" requires both a Postgres DSN and a Weaviate host; a missing
// dependency errors explicitly rather than silently degrading to a single
// backend (the whole point of dual mode is to exercise both stores).
func NewMenuStore(ctx context.Context, cfg MenuStoreConfig) (MenuStore, error) {
	switch cfg.Type {
	case "", "postgres":
		if cfg.PostgresDSN == "" {
			return nil, fmt.Errorf("menu-store=postgres requires --postgres-dsn")
		}
		return search.NewPostgresClient(cfg.PostgresDSN, cfg.Embedder)

	case "weaviate":
		if cfg.WeaviateHost == "" {
			return nil, fmt.Errorf("menu-store=weaviate requires --weaviate-host")
		}
		wc, err := search.NewClient(cfg.WeaviateHost, cfg.WeaviateScheme, cfg.WeaviateAPIKey, cfg.Embedder)
		if err != nil {
			return nil, fmt.Errorf("weaviate client: %w", err)
		}
		if err := wc.EnsureMenuSchema(ctx); err != nil {
			return nil, fmt.Errorf("ensure menu schema: %w", err)
		}
		return wc, nil

	case "dual":
		if cfg.PostgresDSN == "" {
			return nil, fmt.Errorf("menu-store=dual requires --postgres-dsn")
		}
		if cfg.WeaviateHost == "" {
			return nil, fmt.Errorf("menu-store=dual requires --weaviate-host")
		}
		pg, err := search.NewPostgresClient(cfg.PostgresDSN, cfg.Embedder)
		if err != nil {
			return nil, fmt.Errorf("postgres client: %w", err)
		}
		wc, err := search.NewClient(cfg.WeaviateHost, cfg.WeaviateScheme, cfg.WeaviateAPIKey, cfg.Embedder)
		if err != nil {
			return nil, fmt.Errorf("weaviate client: %w", err)
		}
		if err := wc.EnsureMenuSchema(ctx); err != nil {
			slog.Warn("dual menu store: weaviate EnsureMenuSchema failed", "error", err)
		}
		return NewDualMenuStore(pg, wc), nil

	default:
		return nil, fmt.Errorf("unknown --menu-store %q (want postgres|weaviate|dual)", cfg.Type)
	}
}
