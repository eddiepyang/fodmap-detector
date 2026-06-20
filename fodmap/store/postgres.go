// Package store provides the canonical PostgreSQL-backed catalog for FODMAP
// ingredients. It is the source of truth for ingredient metadata; vector
// search indexes are treated as derived projections that are kept in sync on a
// best-effort basis.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"fodmap/data"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
)

// Sentinel errors returned by FodmapCatalogStore.
var (
	// ErrIngredientNotFound is returned when a requested ingredient does not exist.
	ErrIngredientNotFound = errors.New("ingredient not found")
	// ErrIngredientExists is returned when creating an ingredient that already exists.
	ErrIngredientExists = errors.New("ingredient already exists")
)

//go:embed sql/create.sql
var createSQL string

//go:embed sql/get.sql
var getSQL string

//go:embed sql/update.sql
var updateSQL string

//go:embed sql/delete.sql
var deleteSQL string

//go:embed sql/list.sql
var listSQL string

//go:embed sql/list_all.sql
var listAllSQL string

//go:embed sql/count.sql
var countSQL string

//go:embed sql/stats.sql
var statsSQL string

//go:embed sql/seed.sql
var seedSQL string

//go:embed sql/reseed.sql
var reseedSQL string

//go:embed sql/get_meta.sql
var getMetaSQL string

//go:embed sql/set_meta.sql
var setMetaSQL string

// CatalogEntry is a single ingredient row from the canonical catalog.
type CatalogEntry struct {
	Ingredient    string
	Level         string
	Groups        []string
	Notes         string
	Substitutions []string
	UpdatedAt     string
}

// ListFilter holds optional filters for listing/counting ingredients.
type ListFilter struct {
	Search string
	Level  string
	Group  string
}

// LevelStats holds counts grouped by FODMAP level.
type LevelStats map[string]int

// GroupStats holds counts grouped by FODMAP group.
type GroupStats map[string]int

// Stats is the aggregate result returned by Stats.
type Stats struct {
	TotalCount  int
	LevelCounts LevelStats
	GroupCounts GroupStats
}

// FodmapCatalogStore is the canonical PostgreSQL store for FODMAP ingredients.
type FodmapCatalogStore struct {
	db *sql.DB
}

// NewFodmapCatalogStore opens a new PostgreSQL-backed catalog store.
func NewFodmapCatalogStore(dsn string) (*FodmapCatalogStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open PostgreSQL database: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping PostgreSQL database: %w", err)
	}
	return &FodmapCatalogStore{db: db}, nil
}

// EnsureSchema is a no-op. Schema creation is handled by the centralised
// migration runner (internal/db). The method is kept to satisfy the
// CatalogStore interface.
func (s *FodmapCatalogStore) EnsureSchema(_ context.Context) error {
	return nil
}

// Close closes the underlying database connection.
func (s *FodmapCatalogStore) Close() error {
	return s.db.Close()
}

// Create inserts a new ingredient into the catalog.
func (s *FodmapCatalogStore) Create(ctx context.Context, entry CatalogEntry) error {
	if _, err := s.db.ExecContext(ctx, createSQL,
		strings.ToLower(entry.Ingredient),
		entry.Level,
		pq.Array(entry.Groups),
		entry.Notes,
		pq.Array(entry.Substitutions),
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrIngredientExists
		}
		return fmt.Errorf("creating ingredient: %w", err)
	}
	return nil
}

// Ingredient retrieves a single ingredient by name. Returns nil when not found.
func (s *FodmapCatalogStore) Ingredient(ctx context.Context, name string) (*CatalogEntry, error) {
	var entry CatalogEntry
	var updatedAt any
	err := s.db.QueryRowContext(ctx, getSQL, strings.ToLower(name)).Scan(
		&entry.Ingredient,
		&entry.Level,
		(*pgxStringArray)(&entry.Groups),
		&entry.Notes,
		(*pgxStringArray)(&entry.Substitutions),
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting ingredient: %w", err)
	}
	entry.UpdatedAt = fmt.Sprint(updatedAt)
	return &entry, nil
}

// List returns a paginated list of ingredients filtered by the provided filter.
func (s *FodmapCatalogStore) List(ctx context.Context, offset, limit int, filter ListFilter) ([]CatalogEntry, error) {
	args := append(buildListArgs(filter), limit, offset)
	rows, err := s.db.QueryContext(ctx, listSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("listing ingredients: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanEntries(rows)
}

// Count returns the total number of ingredients matching the filter.
func (s *FodmapCatalogStore) Count(ctx context.Context, filter ListFilter) (int, error) {
	args := buildListArgs(filter)
	var total int
	if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("counting ingredients: %w", err)
	}
	return total, nil
}

// Stats returns aggregate counts by level and group.
func (s *FodmapCatalogStore) Stats(ctx context.Context) (*Stats, error) {
	var total int
	var levelJSON, groupJSON []byte
	if err := s.db.QueryRowContext(ctx, statsSQL).Scan(&total, &levelJSON, &groupJSON); err != nil {
		return nil, fmt.Errorf("querying stats: %w", err)
	}
	return &Stats{
		TotalCount:  total,
		LevelCounts: parseCountJSON(levelJSON),
		GroupCounts: parseCountJSON(groupJSON),
	}, nil
}

// Update performs a strict update of an existing ingredient. If the ingredient
// does not exist, ErrIngredientNotFound is returned.
func (s *FodmapCatalogStore) Update(ctx context.Context, name string, entry CatalogEntry) error {
	res, err := s.db.ExecContext(ctx, updateSQL,
		strings.ToLower(name),
		entry.Level,
		pq.Array(entry.Groups),
		entry.Notes,
		pq.Array(entry.Substitutions),
	)
	if err != nil {
		return fmt.Errorf("updating ingredient: %w", err)
	}
	ra, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if ra == 0 {
		return ErrIngredientNotFound
	}
	return nil
}

// Delete removes an ingredient from the catalog. It does not return an error
// when the ingredient does not exist.
func (s *FodmapCatalogStore) Delete(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, deleteSQL, strings.ToLower(name)); err != nil {
		return fmt.Errorf("deleting ingredient: %w", err)
	}
	return nil
}

// ListAll returns every ingredient in the catalog. It is used during startup to
// rebuild the vector search index from the canonical catalog.
func (s *FodmapCatalogStore) ListAll(ctx context.Context) ([]CatalogEntry, error) {
	rows, err := s.db.QueryContext(ctx, listAllSQL)
	if err != nil {
		return nil, fmt.Errorf("listing all ingredients: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanEntries(rows)
}

// IsSeeded returns true if the catalog has already been seeded from the static
// map. Seeding is tracked via a single row in fodmap_meta.
func (s *FodmapCatalogStore) IsSeeded(ctx context.Context) (bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, getMetaSQL, "seeded").Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking seeded marker: %w", err)
	}
	return value == "true", nil
}

// SetSeeded persists the seeded marker.
func (s *FodmapCatalogStore) SetSeeded(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, setMetaSQL, "seeded", "true"); err != nil {
		return fmt.Errorf("setting seeded marker: %w", err)
	}
	return nil
}

// Seed inserts the static FodmapDB map into the catalog in a single
// transaction, skipping duplicates. The seeded marker is set as part of the
// same transaction.
func (s *FodmapCatalogStore) Seed(ctx context.Context, items map[string]data.FodmapEntry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin seed transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, seedSQL)
	if err != nil {
		return fmt.Errorf("prepare seed statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for name, entry := range items {
		groups := entry.Groups
		if groups == nil {
			groups = []string{}
		}
		subs := entry.Substitutions
		if subs == nil {
			subs = []string{}
		}
		if _, err := stmt.ExecContext(ctx,
			strings.ToLower(name),
			entry.Level,
			pq.Array(groups),
			entry.Notes,
			pq.Array(subs),
		); err != nil {
			return fmt.Errorf("seeding ingredient %q: %w", name, err)
		}
	}

	if _, err := tx.ExecContext(ctx, setMetaSQL, "seeded", "true"); err != nil {
		return fmt.Errorf("setting seeded marker: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing seed transaction: %w", err)
	}
	return nil
}

// Reseed upserts the static FodmapDB map into the catalog, overwriting entries
// that already exist. Unlike Seed, it does not skip duplicates and does not
// touch the seeded marker. It returns the number of items processed.
func (s *FodmapCatalogStore) Reseed(ctx context.Context, items map[string]data.FodmapEntry) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin reseed transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, reseedSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare reseed statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	count := 0
	for name, entry := range items {
		groups := entry.Groups
		if groups == nil {
			groups = []string{}
		}
		subs := entry.Substitutions
		if subs == nil {
			subs = []string{}
		}
		if _, err := stmt.ExecContext(ctx,
			strings.ToLower(name),
			entry.Level,
			pq.Array(groups),
			entry.Notes,
			pq.Array(subs),
		); err != nil {
			return 0, fmt.Errorf("reseeding ingredient %q: %w", name, err)
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing reseed transaction: %w", err)
	}
	return count, nil
}

func buildListArgs(filter ListFilter) []any {
	search := strings.TrimSpace(filter.Search)
	if search == "" {
		search = "%%"
	} else {
		search = "%" + search + "%"
	}
	level := filter.Level
	group := filter.Group
	return []any{search, level, group}
}

func scanEntries(rows *sql.Rows) ([]CatalogEntry, error) {
	var entries []CatalogEntry
	for rows.Next() {
		var entry CatalogEntry
		var updatedAt any
		if err := rows.Scan(
			&entry.Ingredient,
			&entry.Level,
			(*pgxStringArray)(&entry.Groups),
			&entry.Notes,
			(*pgxStringArray)(&entry.Substitutions),
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning ingredient: %w", err)
		}
		entry.UpdatedAt = fmt.Sprint(updatedAt)
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseCountJSON(b []byte) map[string]int {
	result := make(map[string]int)
	if len(b) == 0 || string(b) == "null" {
		return result
	}
	// Fall back to url.Values-style parsing to avoid importing encoding/json
	// just for this simple map.
	s := string(b)
	s = strings.Trim(s, "{}")
	if s == "" {
		return result
	}
	pairs := strings.Split(s, ",")
	for _, pair := range pairs {
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.Trim(strings.TrimSpace(parts[0]), "\"")
		var n int
		_, _ = fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &n)
		result[key] = n
	}
	return result
}

// pgxStringArray is a helper to scan Postgres TEXT[] into []string for database/sql using pgx.
type pgxStringArray []string

// Scan implements sql.Scanner.
func (a *pgxStringArray) Scan(src any) error {
	switch v := src.(type) {
	case string:
		parsed, err := parsePGArray(v)
		if err != nil {
			return fmt.Errorf("parsing postgres array: %w", err)
		}
		*a = parsed
		return nil
	case []string:
		*a = v
		return nil
	case nil:
		*a = nil
		return nil
	default:
		return fmt.Errorf("unsupported type %T for string array", src)
	}
}

// parsePGArray parses a PostgreSQL array literal (e.g. {a,"b c","d,e"}) into
// a []string, correctly handling quoted elements that contain commas, spaces, or
// special characters.
func parsePGArray(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" || s == "NULL" {
		return []string{}, nil
	}
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil, fmt.Errorf("invalid postgres array: %q", s)
	}
	inner := s[1 : len(s)-1]
	if inner == "" {
		return []string{}, nil
	}

	var result []string
	var current strings.Builder
	inQuotes := false
	escaped := false

	for i := 0; i < len(inner); i++ {
		ch := inner[i]
		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			inQuotes = !inQuotes
			continue
		}
		if ch == ',' && !inQuotes {
			result = append(result, current.String())
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	result = append(result, current.String())
	return result, nil
}

// ToMap converts a slice of catalog entries to a data.FodmapDB-style map.
func ToMap(entries []CatalogEntry) map[string]data.FodmapEntry {
	m := make(map[string]data.FodmapEntry, len(entries))
	for _, e := range entries {
		m[e.Ingredient] = data.FodmapEntry{
			Level:         e.Level,
			Groups:        e.Groups,
			Notes:         e.Notes,
			Substitutions: e.Substitutions,
		}
	}
	return m
}
