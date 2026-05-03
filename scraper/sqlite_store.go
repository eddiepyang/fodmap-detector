package scraper

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver (already a project dep)
)

// SQLiteStore persists scraped restaurant and menu data to SQLite.
// It doubles as the OSM discovery cache so we don't re-query Overpass on every run.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens (or creates) the scraper SQLite database at the given path.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening scraper db: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}
	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error { return s.db.Close() }

// ---- Schema ----

func (s *SQLiteStore) migrate() error {
	stmts := []string{
		// OSM restaurant discovery cache
		`CREATE TABLE IF NOT EXISTS restaurants (
			osm_id        INTEGER PRIMARY KEY,
			osm_type      TEXT NOT NULL DEFAULT 'node',
			name          TEXT NOT NULL,
			cuisine       TEXT,
			website       TEXT,
			phone         TEXT,
			address       TEXT,
			lat           REAL,
			lon           REAL,
			discovered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			scraped_at    TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_restaurants_name ON restaurants(name);`,

		// Scraped menu results (one row per restaurant scrape run)
		`CREATE TABLE IF NOT EXISTS menus (
			id            TEXT PRIMARY KEY,
			osm_id        INTEGER REFERENCES restaurants(osm_id),
			menu_url      TEXT,
			source        TEXT,  -- "html", "pdf", "vision", "cached"
			raw_html      TEXT,
			scraped_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,

		// Individual menu items extracted from a menu
		`CREATE TABLE IF NOT EXISTS menu_items (
			id          TEXT PRIMARY KEY,
			menu_id     TEXT NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
			name        TEXT NOT NULL,
			description TEXT,
			price       TEXT,
			category    TEXT,
			ingredients TEXT,  -- JSON array
			safety_score TEXT, -- "safe", "caution", "avoid" (set during FODMAP analysis)
			fodmap_flags TEXT  -- JSON array of FodmapFlag
		);`,
		`CREATE INDEX IF NOT EXISTS idx_menu_items_menu ON menu_items(menu_id);`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migration error: %w\nSQL: %s", err, stmt)
		}
	}

	// Additive migrations — safe to re-run.
	migrations := []string{
		"ALTER TABLE restaurants ADD COLUMN scraped_at TIMESTAMP;",
	}
	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			// Non-fatal: column may already exist from a previous run.
			_ = err
		}
	}
	return nil
}

// ---- Restaurant cache (OSM) ----

// UpsertRestaurants stores a batch of OSM restaurants into the local cache.
func (s *SQLiteStore) UpsertRestaurants(ctx context.Context, restaurants []OSMRestaurant) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO restaurants (osm_id, osm_type, name, cuisine, website, phone, address, lat, lon)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(osm_id) DO UPDATE SET
			name=excluded.name, cuisine=excluded.cuisine,
			website=excluded.website, phone=excluded.phone,
			address=excluded.address, lat=excluded.lat, lon=excluded.lon,
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
func (s *SQLiteStore) SearchRestaurants(ctx context.Context, query string) ([]OSMRestaurant, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT osm_id, osm_type, name, cuisine, website, phone, address, lat, lon
		 FROM restaurants WHERE name LIKE ? LIMIT 20`,
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

// ---- Menu storage ----

// SaveMenu persists a scrape result and its menu items.
// Returns the generated menu ID.
func (s *SQLiteStore) SaveMenu(ctx context.Context, osmID int64, result *ScrapeResult) (string, error) {
	menuID := fmt.Sprintf("menu_%d_%d", osmID, time.Now().UnixMilli())

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO menus (id, osm_id, menu_url, source, scraped_at) VALUES (?, ?, ?, ?, ?)`,
		menuID, osmID, result.MenuURL, result.Source, result.ScrapedAt,
	)
	if err != nil {
		return "", fmt.Errorf("inserting menu: %w", err)
	}

	// Update scraped_at on the restaurant row.
	_, _ = tx.ExecContext(ctx,
		`UPDATE restaurants SET scraped_at = ? WHERE osm_id = ?`,
		result.ScrapedAt, osmID,
	)

	itemStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO menu_items (id, menu_id, name, description, price, category, ingredients)
		VALUES (?, ?, ?, ?, ?, ?, ?)
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
func (s *SQLiteStore) SaveAnalysis(ctx context.Context, menuID string, analysis *MenuAnalysis) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for i, item := range analysis.Items {
		flagsJSON, _ := json.Marshal(item.FodmapFlags)
		itemID := fmt.Sprintf("%s_item_%d", menuID, i)
		_, err := tx.ExecContext(ctx,
			`UPDATE menu_items SET safety_score = ?, fodmap_flags = ? WHERE id = ?`,
			item.SafetyScore, string(flagsJSON), itemID,
		)
		if err != nil {
			return fmt.Errorf("updating item analysis: %w", err)
		}
	}
	return tx.Commit()
}
