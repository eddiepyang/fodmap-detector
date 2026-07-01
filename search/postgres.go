package search

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"

	"fodmap/data"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"
)

//go:embed sql
var sqlFS embed.FS

var sqlTemplates = template.Must(template.ParseFS(sqlFS, "sql/*.sql"))

// sqlParams holds the dynamic portions injected into query templates.
type sqlParams struct {
	Where    string // complete WHERE clause, e.g. "WHERE r.city ILIKE $2"
	LimitArg string // positional placeholder for the LIMIT value, e.g. "$3"
}

// renderSQL executes a named SQL template and returns the resulting query string.
func renderSQL(name string, p sqlParams) (string, error) {
	var buf bytes.Buffer
	if err := sqlTemplates.ExecuteTemplate(&buf, name, p); err != nil {
		return "", fmt.Errorf("render sql %s: %w", name, err)
	}
	return buf.String(), nil
}

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

// EnsureSchema is a no-op. Schema creation is handled by the centralised
// migration runner (internal/db). The method is kept to satisfy the Searcher
// interface.
func (c *PostgresClient) EnsureSchema(_ context.Context) error {
	return nil
}

// BatchUpsert inserts or updates a batch of reviews and their chunks in PostgreSQL.
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

	stmtReview, err := tx.PrepareContext(ctx, `
		INSERT INTO reviews (review_id, business_id, business_name, city, state, categories, stars, text) 
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) 
		ON CONFLICT (review_id) DO UPDATE SET 
			business_id = EXCLUDED.business_id,
			business_name = EXCLUDED.business_name,
			city = EXCLUDED.city,
			state = EXCLUDED.state,
			categories = EXCLUDED.categories,
			stars = EXCLUDED.stars,
			text = EXCLUDED.text
	`)
	if err != nil {
		return fmt.Errorf("prepare review stmt: %w", err)
	}
	defer func() { _ = stmtReview.Close() }()

	stmtDelChunks, err := tx.PrepareContext(ctx, `DELETE FROM review_chunks WHERE review_id = $1`)
	if err != nil {
		return fmt.Errorf("prepare delete chunks stmt: %w", err)
	}
	defer func() { _ = stmtDelChunks.Close() }()

	stmtChunk, err := tx.PrepareContext(ctx, `
		INSERT INTO review_chunks (review_id, chunk_text, embedding) 
		VALUES ($1, $2, $3)
	`)
	if err != nil {
		return fmt.Errorf("prepare chunk stmt: %w", err)
	}
	defer func() { _ = stmtChunk.Close() }()

	for _, item := range items {
		var businessID any
		if item.BusinessUUID != nil {
			businessID = *item.BusinessUUID
		}
		if _, err := stmtReview.ExecContext(ctx,
			item.Review.ReviewID,
			businessID,
			item.BusinessName,
			item.City,
			item.State,
			item.Categories,
			item.Review.Stars,
			item.Review.Text,
		); err != nil {
			return fmt.Errorf("insert review %q: %w", item.Review.ReviewID, err)
		}

		if _, err := stmtDelChunks.ExecContext(ctx, item.Review.ReviewID); err != nil {
			return fmt.Errorf("delete old chunks %q: %w", item.Review.ReviewID, err)
		}

		if len(item.Chunks) > 0 {
			for _, chunk := range item.Chunks {
				if _, err := stmtChunk.ExecContext(ctx, item.Review.ReviewID, chunk.Text, pgvector.NewHalfVector(chunk.Vector)); err != nil {
					return fmt.Errorf("insert chunk for %q: %w", item.Review.ReviewID, err)
				}
			}
		} else if item.Vector != nil {
			// Fallback for legacy indexing paths that don't chunk yet
			if _, err := stmtChunk.ExecContext(ctx, item.Review.ReviewID, item.Review.Text, pgvector.NewHalfVector(item.Vector)); err != nil {
				return fmt.Errorf("insert legacy chunk for %q: %w", item.Review.ReviewID, err)
			}
		}
	}

	return tx.Commit()
}

// EnsureFodmapSchema is a no-op. Schema creation is handled by the centralised
// migration runner (internal/db). The method is kept to satisfy the Searcher
// interface.
func (c *PostgresClient) EnsureFodmapSchema(_ context.Context) error {
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
		INSERT INTO fodmap_ingredients (ingredient, level, groups, notes, substitutions, embedding)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (ingredient) DO UPDATE SET
			level = EXCLUDED.level,
			groups = EXCLUDED.groups,
			notes = EXCLUDED.notes,
			substitutions = EXCLUDED.substitutions,
			embedding = EXCLUDED.embedding
	`)
	if err != nil {
		return fmt.Errorf("prepare stmt: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for name, entry := range items {
		// Vectorize the ingredient name
		vec, err := c.embedder.EmbedSingle(ctx, name)
		if err != nil {
			return fmt.Errorf("vectorize %q: %w", name, err)
		}

		groups := entry.Groups
		if groups == nil {
			groups = []string{}
		}
		subs := entry.Substitutions
		if subs == nil {
			subs = []string{}
		}
		if _, err := stmt.ExecContext(ctx, name, entry.Level, pq.Array(groups), entry.Notes, pq.Array(subs), pgvector.NewHalfVector(vec)); err != nil {
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
		SELECT ingredient, level, groups, notes, substitutions, (1 - (embedding <=> $1)) AS certainty
		FROM fodmap_ingredients
		ORDER BY embedding <=> $1
		LIMIT 1
	`

	var res FodmapResult
	var certainty float64
	err = c.db.QueryRowContext(ctx, query, pgvector.NewHalfVector(vec)).Scan(
		&res.Ingredient, &res.Level, (*pgxStringArray)(&res.Groups), &res.Notes, (*pgxStringArray)(&res.Substitutions), &certainty,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FodmapResult{}, 0, fmt.Errorf("not found")
		}
		return FodmapResult{}, 0, fmt.Errorf("query fodmap: %w", err)
	}

	return res, certainty, nil
}

// UpsertFodmapItem embeds and upserts a single ingredient into the Postgres
// vector table. It is used by the admin CRUD handler to keep the search index
// in sync with the canonical catalog.
func (c *PostgresClient) UpsertFodmapItem(ctx context.Context, name string, entry data.FodmapEntry) error {
	vec, err := c.embedder.EmbedSingle(ctx, name)
	if err != nil {
		return fmt.Errorf("vectorize ingredient: %w", err)
	}

	groups := entry.Groups
	if groups == nil {
		groups = []string{}
	}
	subs := entry.Substitutions
	if subs == nil {
		subs = []string{}
	}

	_, err = c.db.ExecContext(ctx, `
		INSERT INTO fodmap_ingredients (ingredient, level, groups, notes, substitutions, embedding)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (ingredient) DO UPDATE SET
			level = EXCLUDED.level,
			groups = EXCLUDED.groups,
			notes = EXCLUDED.notes,
			substitutions = EXCLUDED.substitutions,
			embedding = EXCLUDED.embedding
	`, name, entry.Level, pq.Array(groups), entry.Notes, pq.Array(subs), pgvector.NewHalfVector(vec))
	if err != nil {
		return fmt.Errorf("upsert fodmap item: %w", err)
	}
	return nil
}

// DeleteFodmapItem removes a single ingredient from the Postgres vector table.
func (c *PostgresClient) DeleteFodmapItem(ctx context.Context, name string) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM fodmap_ingredients WHERE ingredient = $1`, name)
	if err != nil {
		return fmt.Errorf("delete fodmap item: %w", err)
	}
	return nil
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

// Businesses performs an aggregation-like search by querying reviews and grouping by business.
func (c *PostgresClient) Businesses(ctx context.Context, query string, limit int, filter SearchFilter) (SearchResult, error) {
	vec, err := c.embedder.EmbedSingle(ctx, query)
	if err != nil {
		return SearchResult{}, fmt.Errorf("vectorize query: %w", err)
	}

	// Base query parts
	whereClauses := []string{}
	args := []any{pgvector.NewHalfVector(vec)}
	argID := 2

	if filter.Category != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("r.categories ILIKE $%d", argID))
		args = append(args, "%"+filter.Category+"%")
		argID++
	}
	if filter.City != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("r.city ILIKE $%d", argID))
		args = append(args, "%"+filter.City+"%")
		argID++
	}
	if filter.State != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("r.state ILIKE $%d", argID))
		args = append(args, filter.State)
		argID++
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	sqlQuery, err := renderSQL("get_businesses.sql", sqlParams{Where: whereSQL, LimitArg: fmt.Sprintf("$%d", argID)})
	if err != nil {
		return SearchResult{}, err
	}

	args = append(args, limit)

	rows, err := c.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return SearchResult{}, fmt.Errorf("query businesses: %w", err)
	}
	defer func() { _ = rows.Close() }()

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

// Reviews retrieves top reviews for a query, filtered by business if specified.
func (c *PostgresClient) Reviews(ctx context.Context, query string, limit int, filter SearchFilter) (SearchReviews, error) {
	vec, err := c.embedder.EmbedSingle(ctx, query)
	if err != nil {
		return SearchReviews{}, fmt.Errorf("vectorize query: %w", err)
	}

	whereClauses := []string{}
	args := []any{pgvector.NewHalfVector(vec)}
	argID := 2

	if filter.BusinessID != uuid.Nil {
		whereClauses = append(whereClauses, fmt.Sprintf("r.business_id = $%d", argID))
		args = append(args, filter.BusinessID)
		argID++
	}
	if filter.City != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("r.city ILIKE $%d", argID))
		args = append(args, "%"+filter.City+"%")
		argID++
	}
	if filter.State != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("r.state ILIKE $%d", argID))
		args = append(args, filter.State)
		argID++
	}
	if len(filter.ReviewIDs) > 0 {
		// Use ANY for array check
		whereClauses = append(whereClauses, fmt.Sprintf("r.review_id = ANY($%d)", argID))
		// pq/pgx allows passing []string to ANY
		args = append(args, pq.Array(filter.ReviewIDs))
		argID++
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	sqlQuery, err := renderSQL("get_reviews.sql", sqlParams{Where: whereSQL, LimitArg: fmt.Sprintf("$%d", argID)})
	if err != nil {
		return SearchReviews{}, err
	}

	args = append(args, limit)

	rows, err := c.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return SearchReviews{}, fmt.Errorf("query reviews: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var reviews []RankedReview
	for rows.Next() {
		var r RankedReview
		var city, state sql.NullString
		if err := rows.Scan(
			&r.Review.Review.ReviewID,
			&r.Review.Review.BusinessID,
			&r.Review.BusinessName,
			&city,
			&state,
			&r.Review.Review.Text,
			&r.MatchedChunk,
			&r.Score,
		); err != nil {
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

// EnsureMenuSchema is a no-op because migrations handle the schema.
func (c *PostgresClient) EnsureMenuSchema(_ context.Context) error {
	return nil
}

// BatchUpsertMenu upserts menu items to the Postgres menu_items table.
func (c *PostgresClient) BatchUpsertMenu(ctx context.Context, items []MenuItem) error {
	if len(items) == 0 {
		return nil
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO menu_items (menu_item_id, business_id, menu_section, restaurant_name, city, state, dish_name, description, price, stated_ingredients, has_full_ingredients, modifiers, source_url, address, phone_number, scraped_at, embedding, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, NOW(), NOW())
		ON CONFLICT (menu_item_id) DO UPDATE SET
			business_id = EXCLUDED.business_id,
			menu_section = EXCLUDED.menu_section,
			restaurant_name = EXCLUDED.restaurant_name,
			city = EXCLUDED.city,
			state = EXCLUDED.state,
			dish_name = EXCLUDED.dish_name,
			description = EXCLUDED.description,
			price = EXCLUDED.price,
			stated_ingredients = EXCLUDED.stated_ingredients,
			has_full_ingredients = EXCLUDED.has_full_ingredients,
			modifiers = EXCLUDED.modifiers,
			source_url = EXCLUDED.source_url,
			address = EXCLUDED.address,
			phone_number = EXCLUDED.phone_number,
			scraped_at = EXCLUDED.scraped_at,
			embedding = EXCLUDED.embedding
	`)
	if err != nil {
		return fmt.Errorf("prepare stmt: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, item := range items {
		var vec any
		if item.Vector != nil {
			vec = pgvector.NewHalfVector(item.Vector)
		}
		modifiersJSON, _ := json.Marshal(item.Modifiers)
		if _, err := stmt.ExecContext(ctx, item.MenuItemID, item.BusinessID, item.MenuSection, item.RestaurantName, item.City, item.State, item.DishName, item.Description, item.Price, pq.Array(item.StatedIngredients), item.HasFullIngredients, modifiersJSON, item.SourceURL, item.Address, item.PhoneNumber, item.ScrapedAt, vec); err != nil {
			return fmt.Errorf("insert menu item %q: %w", item.MenuItemID, err)
		}
	}

	return tx.Commit()
}

// SearchMenu searches the Postgres menu_items table using cosine distance.
func (c *PostgresClient) SearchMenu(ctx context.Context, query string, limit int) ([]MenuItem, error) {
	vec, err := c.embedder.EmbedSingle(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("vectorize query: %w", err)
	}

	q := `
		SELECT menu_item_id, business_id, menu_section, restaurant_name, city, state, dish_name, description, price, stated_ingredients, has_full_ingredients, modifiers, source_url, address, phone_number, scraped_at
		FROM menu_items
		ORDER BY embedding <=> $1
		LIMIT $2
	`
	rows, err := c.db.QueryContext(ctx, q, pgvector.NewHalfVector(vec), limit)
	if err != nil {
		return nil, fmt.Errorf("query menu items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []MenuItem
	for rows.Next() {
		var m MenuItem
		var sec, restName, city, state, desc, sourceURL, address, phone, scrapedAt sql.NullString
		var price sql.NullFloat64
		var modifiersJSON []byte
		if err := rows.Scan(&m.MenuItemID, &m.BusinessID, &sec, &restName, &city, &state, &m.DishName, &desc, &price, (*pgxStringArray)(&m.StatedIngredients), &m.HasFullIngredients, &modifiersJSON, &sourceURL, &address, &phone, &scrapedAt); err != nil {
			return nil, fmt.Errorf("scan menu item: %w", err)
		}
		m.MenuSection = sec.String
		m.RestaurantName = restName.String
		m.City = city.String
		m.State = state.String
		m.Description = desc.String
		if price.Valid {
			p := price.Float64
			m.Price = &p
		}
		if len(modifiersJSON) > 0 && string(modifiersJSON) != "null" {
			_ = json.Unmarshal(modifiersJSON, &m.Modifiers)
		}
		m.SourceURL = sourceURL.String
		m.Address = address.String
		m.PhoneNumber = phone.String
		m.ScrapedAt = scrapedAt.String
		results = append(results, m)
	}
	return results, nil
}
