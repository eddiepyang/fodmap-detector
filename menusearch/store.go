package menusearch

import (
	"context"
	_ "embed"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fodmap/server"
)

//go:embed store/sql/upsert_restaurant.sql
var upsertRestaurantSQL string

//go:embed store/sql/upsert_restaurant_by_yelp.sql
var upsertRestaurantByYelpSQL string

//go:embed store/sql/get_restaurant.sql
var getRestaurantSQL string

//go:embed store/sql/get_restaurant_by_id.sql
var getRestaurantByIDSQL string

//go:embed store/sql/list_restaurants.sql
var listRestaurantsSQL string

//go:embed store/sql/update_discovery_urls.sql
var updateDiscoveryURLsSQL string

//go:embed store/sql/update_scrape_result.sql
var updateScrapeResultSQL string

//go:embed store/sql/set_extraction_tier.sql
var setExtractionTierSQL string

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Upsert(ctx context.Context, r server.Restaurant) (*server.Restaurant, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, upsertRestaurantSQL,
		r.CAMIS, r.DBA, r.Boro, r.Building, r.Street, r.Zipcode, r.Phone, r.Address, r.Cuisine, r.Latitude, r.Longitude, r.NTA, r.Status,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	r.ID = id
	return &r, nil
}

func (s *Store) UpsertByYelp(ctx context.Context, yelpID, dba string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, upsertRestaurantByYelpSQL, yelpID, dba).Scan(&id)
	return id, err
}

func (s *Store) Get(ctx context.Context, camis string) (*server.Restaurant, error) {
	var r server.Restaurant
	err := s.pool.QueryRow(ctx, getRestaurantSQL, camis).Scan(
		&r.ID, &r.CAMIS, &r.YelpID, &r.DBA, &r.Boro, &r.Building, &r.Street, &r.Zipcode, &r.Phone, &r.Address, &r.Cuisine, &r.Latitude, &r.Longitude, &r.NTA,
		&r.Status, &r.WebsiteURL, &r.MenuURLs, &r.URLSource, &r.ItemCount, &r.ScrapedAt, &r.LastError, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (*server.Restaurant, error) {
	var r server.Restaurant
	err := s.pool.QueryRow(ctx, getRestaurantByIDSQL, id).Scan(
		&r.ID, &r.CAMIS, &r.YelpID, &r.DBA, &r.Boro, &r.Building, &r.Street, &r.Zipcode, &r.Phone, &r.Address, &r.Cuisine, &r.Latitude, &r.Longitude, &r.NTA,
		&r.Status, &r.WebsiteURL, &r.MenuURLs, &r.URLSource, &r.ItemCount, &r.ScrapedAt, &r.LastError, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (s *Store) List(ctx context.Context, status string, search string, limit, offset int) ([]server.Restaurant, error) {
	rows, err := s.pool.Query(ctx, listRestaurantsSQL, status, search, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []server.Restaurant
	for rows.Next() {
		var r server.Restaurant
		if err := rows.Scan(
			&r.ID, &r.CAMIS, &r.YelpID, &r.DBA, &r.Boro, &r.Building, &r.Street, &r.Zipcode, &r.Phone, &r.Address, &r.Cuisine, &r.Latitude, &r.Longitude, &r.NTA,
			&r.Status, &r.WebsiteURL, &r.MenuURLs, &r.URLSource, &r.ItemCount, &r.ScrapedAt, &r.LastError, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *Store) UpdateDiscoveryURLs(ctx context.Context, camis, websiteURL string, menuURLs []string, source, address, phone string) error {
	_, err := s.pool.Exec(ctx, updateDiscoveryURLsSQL, camis, websiteURL, menuURLs, source, address, phone)
	return err
}

func (s *Store) UpdateScrapeResult(ctx context.Context, camis, status string, itemCount int, lastError string) error {
	_, err := s.pool.Exec(ctx, updateScrapeResultSQL, camis, status, itemCount, lastError)
	return err
}

// SetExtractionTier records which cascade tier produced a successful scrape
// (see pipeline.Tier* constants) for tier-mix telemetry. An empty tier clears
// the column. Best-effort: telemetry only, never blocks the scrape result.
func (s *Store) SetExtractionTier(ctx context.Context, camis, tier string) error {
	_, err := s.pool.Exec(ctx, setExtractionTierSQL, camis, tier)
	return err
}

func (s *Store) MaxUpdatedAt(ctx context.Context) (time.Time, error) {
	var maxTime time.Time
	err := s.pool.QueryRow(ctx, "SELECT COALESCE(MAX(updated_at), '1970-01-01'::timestamp) FROM restaurants").Scan(&maxTime)
	return maxTime, err
}
