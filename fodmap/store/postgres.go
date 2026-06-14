// Package store provides the canonical PostgreSQL-backed catalog for FODMAP
// ingredients. It is the source of truth for ingredient metadata; vector
// search indexes are treated as derived projections that are kept in sync on a
// best-effort basis.
package store

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"
	"text/template"

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

//go:embed sql/*.sql
var sqlFS embed.FS

var sqlTemplates = template.Must(template.ParseFS(sqlFS, "sql/*.sql"))

type sqlParams struct {
	Where     string
	LimitArg  string
	OffsetArg string
}

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

// Get retrieves a single ingredient by name. Returns nil when not found.
func (s *FodmapCatalogStore) Get(ctx context.Context, name string) (*CatalogEntry, error) {
	var entry CatalogEntry
	var updatedAt interface{}
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
	where, args, err := buildListWhere(filter)
	if err != nil {
		return nil, err
	}
	query, err := renderSQL("list.sql", sqlParams{
		Where:     where,
		LimitArg:  fmt.Sprintf("$%d", len(args)+1),
		OffsetArg: fmt.Sprintf("$%d", len(args)+2),
	})
	if err != nil {
		return nil, err
	}
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing ingredients: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanEntries(rows)
}

// Count returns the total number of ingredients matching the filter.
func (s *FodmapCatalogStore) Count(ctx context.Context, filter ListFilter) (int, error) {
	where, args, err := buildListWhere(filter)
	if err != nil {
		return 0, err
	}
	query, err := renderSQL("count.sql", sqlParams{Where: where})
	if err != nil {
		return 0, err
	}
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
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
		if _, err := stmt.ExecContext(ctx,
			strings.ToLower(name),
			entry.Level,
			pq.Array(entry.Groups),
			entry.Notes,
			pq.Array(entry.Substitutions),
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

// embedded SQL strings
var (
	createSQL  = mustRead("sql/create.sql")
	getSQL     = mustRead("sql/get.sql")
	updateSQL  = mustRead("sql/update.sql")
	deleteSQL  = mustRead("sql/delete.sql")
	listAllSQL = mustRead("sql/list_all.sql")
	statsSQL   = mustRead("sql/stats.sql")
	seedSQL    = mustRead("sql/seed.sql")
	getMetaSQL = mustRead("sql/get_meta.sql")
	setMetaSQL = mustRead("sql/set_meta.sql")
)

func mustRead(name string) string {
	b, err := sqlFS.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("failed to read embedded sql %q: %v", name, err))
	}
	return string(b)
}

func renderSQL(name string, p sqlParams) (string, error) {
	var buf bytes.Buffer
	if err := sqlTemplates.ExecuteTemplate(&buf, name, p); err != nil {
		return "", fmt.Errorf("render sql %s: %w", name, err)
	}
	return buf.String(), nil
}

func buildListWhere(filter ListFilter) (string, []any, error) {
	var clauses []string
	var args []any
	argID := 1

	search := strings.TrimSpace(filter.Search)
	if search != "" {
		clauses = append(clauses, fmt.Sprintf("(ingredient ILIKE $%d OR notes ILIKE $%d)", argID, argID))
		args = append(args, "%"+search+"%")
		argID++
	}
	if filter.Level != "" {
		clauses = append(clauses, fmt.Sprintf("level = $%d", argID))
		args = append(args, filter.Level)
		argID++
	}
	if filter.Group != "" {
		clauses = append(clauses, fmt.Sprintf("$%d = ANY(groups)", argID))
		args = append(args, filter.Group)
	}

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}
	return where, args, nil
}

func scanEntries(rows *sql.Rows) ([]CatalogEntry, error) {
	var entries []CatalogEntry
	for rows.Next() {
		var entry CatalogEntry
		var updatedAt interface{}
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
		s := strings.Trim(v, "{}")
		if s == "" {
			*a = []string{}
			return nil
		}
		*a = strings.Split(s, ",")
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
