package search

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"fodmap/data"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"
)

// PostgresClient implements Searcher for PostgreSQL with pgvector.
type PostgresClient struct {
	db       *sql.DB
	embedder Embedder
}

// NewPostgresClient creates a new PostgresClient.
func NewPostgresClient(dsn string, e Embedder) (*PostgresClient, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open PostgreSQL database: %w", err)
	}

	// Verify connection
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping PostgreSQL database: %w", err)
	}

	return &PostgresClient{
		db:       db,
		embedder: e,
	}, nil
}

// EnsureSchema creates the pgvector extension and the required tables for vectors.
func (c *PostgresClient) EnsureSchema(ctx context.Context) error {
	queries := []string{
		`CREATE EXTENSION IF NOT EXISTS vector;`,
		`CREATE TABLE IF NOT EXISTS reviews (
			review_id TEXT PRIMARY KEY,
			business_id TEXT,
			business_name TEXT,
			city TEXT,
			state TEXT,
			categories TEXT,
			stars FLOAT,
			text TEXT,
			embedding vector(768)
		);`,
		// We use half-precision (if supported) or regular hnsw.
		// `vector_cosine_ops` creates an index optimized for <=> cosine distance.
		`CREATE INDEX IF NOT EXISTS idx_reviews_embedding ON reviews USING hnsw (embedding vector_cosine_ops);`,
	}

	for _, query := range queries {
		if _, err := c.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to execute schema query %q: %w", query, err)
		}
	}
	return nil
}

// BatchUpsert inserts or updates a batch of reviews in PostgreSQL.
func (c *PostgresClient) BatchUpsert(ctx context.Context, items []IndexItem) error {
	if len(items) == 0 {
		return nil
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO reviews (review_id, business_id, business_name, city, state, categories, stars, text, embedding) 
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) 
		ON CONFLICT (review_id) DO UPDATE SET 
			business_id = EXCLUDED.business_id,
			business_name = EXCLUDED.business_name,
			city = EXCLUDED.city,
			state = EXCLUDED.state,
			categories = EXCLUDED.categories,
			stars = EXCLUDED.stars,
			text = EXCLUDED.text,
			embedding = EXCLUDED.embedding
	`)
	if err != nil {
		return fmt.Errorf("prepare stmt: %w", err)
	}
	defer stmt.Close()

	for _, item := range items {
		if _, err := stmt.ExecContext(ctx,
			item.Review.ReviewID,
			item.Review.BusinessID,
			item.BusinessName,
			item.City,
			item.State,
			item.Categories,
			item.Review.Stars,
			item.Review.Text,
			pgvector.NewVector(item.Vector),
		); err != nil {
			return fmt.Errorf("insert review %q: %w", item.Review.ReviewID, err)
		}
	}

	return tx.Commit()
}

// EnsureFodmapSchema creates the table for FODMAP vectors.
func (c *PostgresClient) EnsureFodmapSchema(ctx context.Context) error {
	queries := []string{
		`CREATE EXTENSION IF NOT EXISTS vector;`,
		`CREATE TABLE IF NOT EXISTS fodmap_ingredients (
			ingredient TEXT PRIMARY KEY,
			level TEXT,
			groups TEXT[],
			notes TEXT,
			embedding vector(768)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_fodmap_embedding ON fodmap_ingredients USING hnsw (embedding vector_cosine_ops);`,
	}

	for _, query := range queries {
		if _, err := c.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to execute fodmap schema query: %w", err)
		}
	}
	return nil
}

// BatchUpsertFodmap vectorizes and uploads FODMAP data to PostgreSQL.
func (c *PostgresClient) BatchUpsertFodmap(ctx context.Context, items map[string]data.FodmapEntry) error {
	if len(items) == 0 {
		return nil
	}

	// Begin transaction
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO fodmap_ingredients (ingredient, level, groups, notes, embedding) 
		VALUES ($1, $2, $3, $4, $5) 
		ON CONFLICT (ingredient) DO UPDATE SET 
			level = EXCLUDED.level, 
			groups = EXCLUDED.groups, 
			notes = EXCLUDED.notes, 
			embedding = EXCLUDED.embedding
	`)
	if err != nil {
		return fmt.Errorf("prepare stmt: %w", err)
	}
	defer stmt.Close()

	for name, entry := range items {
		// Vectorize the ingredient name
		vec, err := c.embedder.EmbedSingle(ctx, name)
		if err != nil {
			return fmt.Errorf("vectorize %q: %w", name, err)
		}

		if _, err := stmt.ExecContext(ctx, name, entry.Level, pq.Array(entry.Groups), entry.Notes, pgvector.NewVector(vec)); err != nil {
			return fmt.Errorf("insert fodmap %q: %w", name, err)
		}
	}

	return tx.Commit()
}

// SearchFodmap looks up an ingredient in the fodmap_ingredients table using cosine distance.
func (c *PostgresClient) SearchFodmap(ctx context.Context, ingredient string) (FodmapResult, float64, error) {
	vec, err := c.embedder.EmbedSingle(ctx, ingredient)
	if err != nil {
		return FodmapResult{}, 0, fmt.Errorf("vectorize ingredient: %w", err)
	}

	query := `
		SELECT ingredient, level, groups, notes, (1 - (embedding <=> $1)) AS certainty
		FROM fodmap_ingredients
		ORDER BY embedding <=> $1
		LIMIT 1
	`

	var res FodmapResult
	var certainty float64
	// In pgx v5 array of string usually scans into []string if supported by database/sql via lib/pq or pgx/stdlib
	// However, we can use a string fallback or generic scan if needed, but let's assume it works for pq/pgx arrays.
	err = c.db.QueryRowContext(ctx, query, pgvector.NewVector(vec)).Scan(
		&res.Ingredient, &res.Level, (*pgxStringArray)(&res.Groups), &res.Notes, &certainty,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return FodmapResult{}, 0, fmt.Errorf("not found")
		}
		return FodmapResult{}, 0, fmt.Errorf("query fodmap: %w", err)
	}

	return res, certainty, nil
}

// pgxStringArray is a helper to scan Postgres TEXT[] into []string for database/sql using pgx
type pgxStringArray []string

func (a *pgxStringArray) Scan(src any) error {
	// pgx/v5 stdlib handles array scanning natively if passed a slice pointer,
	// but using a wrapper ensures interface satisfaction if needed.
	// Actually, pq/pgx supports scanning directly into *[]string. We will just wrap it to be safe,
	// or we can remove the wrapper and pass `&res.Groups` directly.
	switch v := src.(type) {
	case string: // fallback parsing if it comes as string '{a,b,c}'
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
		// Attempt direct scan if pgx supports it
		return fmt.Errorf("unsupported type %T for string array", src)
	}
}

// GetBusinesses performs an aggregation-like search by querying reviews and grouping by business.
func (c *PostgresClient) GetBusinesses(ctx context.Context, query string, limit int, filter SearchFilter) (SearchResult, error) {
	vec, err := c.embedder.EmbedSingle(ctx, query)
	if err != nil {
		return SearchResult{}, fmt.Errorf("vectorize query: %w", err)
	}

	// Base query parts
	whereClauses := []string{}
	args := []any{pgvector.NewVector(vec)}
	argID := 2

	if filter.Category != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("categories ILIKE $%d", argID))
		args = append(args, "%"+filter.Category+"%")
		argID++
	}
	if filter.City != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("city ILIKE $%d", argID))
		args = append(args, "%"+filter.City+"%")
		argID++
	}
	if filter.State != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("state ILIKE $%d", argID))
		args = append(args, filter.State)
		argID++
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	// This is a simplified version of Top-K aggregation.
	// In Postgres, we find the closest reviews, then group by business_id,
	// averaging the top scores. To do this efficiently:
	sqlQuery := fmt.Sprintf(`
		WITH top_reviews AS (
			SELECT business_id, business_name, city, state, categories, stars,
			       (1 - (embedding <=> $1)) AS certainty,
				   ROW_NUMBER() OVER(PARTITION BY business_id ORDER BY embedding <=> $1) as rn
			FROM reviews
			%s
		),
		avg_scores AS (
			SELECT business_id, MAX(business_name) as name, MAX(city) as city, MAX(state) as state,
			       MAX(categories) as categories, AVG(stars) as avg_stars, AVG(certainty) as avg_certainty
			FROM top_reviews
			WHERE rn <= 5
			GROUP BY business_id
		)
		SELECT business_id, name, city, state, categories, avg_stars, avg_certainty
		FROM avg_scores
		ORDER BY avg_certainty DESC
		LIMIT $%d
	`, whereSQL, argID)

	args = append(args, limit)

	rows, err := c.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return SearchResult{}, fmt.Errorf("query businesses: %w", err)
	}
	defer rows.Close()

	var businesses []BusinessResult
	for rows.Next() {
		var b BusinessResult
		var stars sql.NullFloat64
		var categories sql.NullString
		if err := rows.Scan(&b.ID, &b.Name, &b.City, &b.State, &categories, &stars, &b.Score); err != nil {
			return SearchResult{}, fmt.Errorf("scan business: %w", err)
		}
		if stars.Valid {
			b.Stars = stars.Float64
		}
		if categories.Valid {
			b.Categories = categories.String
		}
		businesses = append(businesses, b)
	}
	return SearchResult{Businesses: businesses}, nil
}

// GetReviews retrieves top reviews for a query, filtered by business if specified.
func (c *PostgresClient) GetReviews(ctx context.Context, query string, limit int, filter SearchFilter) (SearchReviews, error) {
	vec, err := c.embedder.EmbedSingle(ctx, query)
	if err != nil {
		return SearchReviews{}, fmt.Errorf("vectorize query: %w", err)
	}

	whereClauses := []string{}
	args := []any{pgvector.NewVector(vec)}
	argID := 2

	if filter.BusinessID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("business_id = $%d", argID))
		args = append(args, filter.BusinessID)
		argID++
	}
	if filter.City != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("city ILIKE $%d", argID))
		args = append(args, "%"+filter.City+"%")
		argID++
	}
	if filter.State != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("state ILIKE $%d", argID))
		args = append(args, filter.State)
		argID++
	}
	if len(filter.ReviewIDs) > 0 {
		// Use ANY for array check
		whereClauses = append(whereClauses, fmt.Sprintf("review_id = ANY($%d)", argID))
		// pq/pgx allows passing []string to ANY
		args = append(args, pq.Array(filter.ReviewIDs))
		argID++
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	sqlQuery := fmt.Sprintf(`
		SELECT review_id, business_id, business_name, city, state, text, (1 - (embedding <=> $1)) AS certainty
		FROM reviews
		%s
		ORDER BY embedding <=> $1
		LIMIT $%d
	`, whereSQL, argID)

	args = append(args, limit)

	rows, err := c.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return SearchReviews{}, fmt.Errorf("query reviews: %w", err)
	}
	defer rows.Close()

	var reviews []RankedReview
	for rows.Next() {
		var r RankedReview
		var city, state sql.NullString
		if err := rows.Scan(&r.Review.Review.ReviewID, &r.Review.Review.BusinessID, &r.Review.BusinessName, &city, &state, &r.Review.Review.Text, &r.Score); err != nil {
			return SearchReviews{}, fmt.Errorf("scan review: %w", err)
		}
		if city.Valid {
			r.Review.City = city.String
		}
		if state.Valid {
			r.Review.State = state.String
		}
		reviews = append(reviews, r)
	}
	return SearchReviews{BusinessReviews: reviews}, nil
}
