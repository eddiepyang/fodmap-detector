package scraper

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresStore persists scraped restaurant and menu data to PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore opens the scraper PostgreSQL database.
func NewPostgresStore(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening postgres db: %w", err)
	}
	s := &PostgresStore{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database connection.
func (s *PostgresStore) Close() error { return s.db.Close() }

func (s *PostgresStore) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS restaurants (
			osm_id        BIGINT PRIMARY KEY,
			osm_type      TEXT NOT NULL DEFAULT 'node',
			name          TEXT NOT NULL,
			cuisine       TEXT,
			website       TEXT,
			phone         TEXT,
			address       TEXT,
			lat           DOUBLE PRECISION,
			lon           DOUBLE PRECISION,
			discovered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			scraped_at    TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_restaurants_name ON restaurants(name);`,

		`CREATE TABLE IF NOT EXISTS menus (
			id            TEXT PRIMARY KEY,
			osm_id        BIGINT REFERENCES restaurants(osm_id),
			menu_url      TEXT,
			source        TEXT,
			raw_html      TEXT,
			scraped_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,

		`CREATE TABLE IF NOT EXISTS menu_items (
			id          TEXT PRIMARY KEY,
			menu_id     TEXT NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
			name        TEXT NOT NULL,
			description TEXT,
			price       TEXT,
			category    TEXT,
			ingredients TEXT,
			safety_score TEXT,
			fodmap_flags TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_menu_items_menu ON menu_items(menu_id);`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migration error: %w\nSQL: %s", err, stmt)
		}
	}
	return nil
}

// UpsertRestaurants stores a batch of OSM restaurants into the local cache.
func (s *PostgresStore) UpsertRestaurants(ctx context.Context, restaurants []OSMRestaurant) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO restaurants (osm_id, osm_type, name, cuisine, website, phone, address, lat, lon)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT(osm_id) DO UPDATE SET
			name=EXCLUDED.name, cuisine=EXCLUDED.cuisine,
			website=EXCLUDED.website, phone=EXCLUDED.phone,
			address=EXCLUDED.address, lat=EXCLUDED.lat, lon=EXCLUDED.lon,
			discovered_at=CURRENT_TIMESTAMP
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, r := range restaurants {
		if _, err := stmt.ExecContext(ctx,
			r.ID, r.Type, r.Name(), r.Cuisine(), r.Website(), r.Phone(), r.Address(), r.Lat, r.Lon,
		); err != nil {
			return fmt.Errorf("upserting restaurant %q: %w", r.Name(), err)
		}
	}
	return tx.Commit()
}

// SearchRestaurants returns cached restaurants whose name contains the query string.
func (s *PostgresStore) SearchRestaurants(ctx context.Context, query string) ([]OSMRestaurant, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT osm_id, osm_type, name, cuisine, website, phone, address, lat, lon
		 FROM restaurants WHERE name ILIKE $1 LIMIT 20`,
		"%"+query+"%",
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []OSMRestaurant
	for rows.Next() {
		var r OSMRestaurant
		r.Tags = make(map[string]string)
		var name string
		var cuisine, website, phone, address sql.NullString
		var lat, lon sql.NullFloat64
		var osmType string
		if err := rows.Scan(&r.ID, &osmType, &name, &cuisine, &website, &phone, &address, &lat, &lon); err != nil {
			return nil, err
		}
		r.Tags["name"] = name
		r.Type = osmType
		if cuisine.Valid {
			r.Tags["cuisine"] = cuisine.String
		}
		if website.Valid {
			r.Tags["website"] = website.String
		}
		if phone.Valid {
			r.Tags["phone"] = phone.String
		}
		if lat.Valid {
			r.Lat = lat.Float64
		}
		if lon.Valid {
			r.Lon = lon.Float64
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// SaveMenu persists a scrape result and its menu items.
func (s *PostgresStore) SaveMenu(ctx context.Context, osmID int64, result *ScrapeResult) (string, error) {
	menuID := fmt.Sprintf("menu_%d_%d", osmID, time.Now().UnixMilli())

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO menus (id, osm_id, menu_url, source, scraped_at) VALUES ($1, $2, $3, $4, $5)`,
		menuID, osmID, result.MenuURL, result.Source, result.ScrapedAt,
	)
	if err != nil {
		return "", fmt.Errorf("inserting menu: %w", err)
	}

	_, _ = tx.ExecContext(ctx,
		`UPDATE restaurants SET scraped_at = $1 WHERE osm_id = $2`,
		result.ScrapedAt, osmID,
	)

	itemStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO menu_items (id, menu_id, name, description, price, category, ingredients)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`)
	if err != nil {
		return "", err
	}
	defer func() { _ = itemStmt.Close() }()

	for i, item := range result.Items {
		itemID := fmt.Sprintf("%s_item_%d", menuID, i)
		ingJSON, _ := json.Marshal(item.Ingredients)
		if _, err := itemStmt.ExecContext(ctx,
			itemID, menuID, item.Name, item.Description, item.Price, item.Category, string(ingJSON),
		); err != nil {
			return "", fmt.Errorf("inserting menu item %q: %w", item.Name, err)
		}
	}

	return menuID, tx.Commit()
}

// SaveAnalysis updates menu_items with FODMAP analysis results.
func (s *PostgresStore) SaveAnalysis(ctx context.Context, menuID string, analysis *MenuAnalysis) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for i, item := range analysis.Items {
		flagsJSON, _ := json.Marshal(item.FodmapFlags)
		itemID := fmt.Sprintf("%s_item_%d", menuID, i)
		_, err := tx.ExecContext(ctx,
			`UPDATE menu_items SET safety_score = $1, fodmap_flags = $2 WHERE id = $3`,
			item.SafetyScore, string(flagsJSON), itemID,
		)
		if err != nil {
			return fmt.Errorf("updating item analysis: %w", err)
		}
	}
	return tx.Commit()
}
