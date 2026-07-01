package menusearch

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
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

//go:embed store/sql/status_counts.sql
var statusCountsSQL string

//go:embed store/sql/count_restaurants.sql
var countRestaurantsSQL string

//go:embed store/sql/tier_counts.sql
var tierCountsSQL string

//go:embed store/sql/failure_counts.sql
var failureCountsSQL string

//go:embed store/sql/job_counts.sql
var jobCountsSQL string

type Store struct {
	pool *pgxpool.Pool
	// riverSchema is the Postgres schema holding River's job tables, used by
	// PipelineStats to report queue depth. Matches the --river-schema flag.
	riverSchema string
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, riverSchema: "river"}
}

// SetRiverSchema overrides the schema queried for River job counts when the
// deployment uses a non-default --river-schema.
func (s *Store) SetRiverSchema(schema string) {
	if schema != "" {
		s.riverSchema = schema
	}
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
		&r.Status, &r.WebsiteURL, &r.MenuURLs, &r.URLSource, &r.ExtractionTier, &r.ItemCount, &r.ScrapedAt, &r.LastError, &r.CreatedAt, &r.UpdatedAt,
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
		&r.Status, &r.WebsiteURL, &r.MenuURLs, &r.URLSource, &r.ExtractionTier, &r.ItemCount, &r.ScrapedAt, &r.LastError, &r.CreatedAt, &r.UpdatedAt,
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
			&r.Status, &r.WebsiteURL, &r.MenuURLs, &r.URLSource, &r.ExtractionTier, &r.ItemCount, &r.ScrapedAt, &r.LastError, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// Count returns the number of restaurants matching the same status/search
// filters as List, so callers can paginate over the full result set.
func (s *Store) Count(ctx context.Context, status string, search string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, countRestaurantsSQL, status, search).Scan(&count)
	return count, err
}

// PipelineStats aggregates the rollup for the admin scraper-pipeline page:
// status counts, total menu items, extraction-tier mix, a failure taxonomy
// bucketed from last_error, and River job counts by kind and state. Job
// counts are best-effort — if the River schema is absent (server running
// without the pipeline), they are empty rather than failing the whole call.
func (s *Store) PipelineStats(ctx context.Context) (*server.PipelineStats, error) {
	stats := &server.PipelineStats{
		StatusCounts:  make(map[string]int),
		TierCounts:    make(map[string]server.TierStat),
		FailureCounts: make(map[string]int),
		JobCounts:     make(map[string]map[string]int),
	}

	rows, err := s.pool.Query(ctx, statusCountsSQL)
	if err != nil {
		return nil, fmt.Errorf("status counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan status counts: %w", err)
		}
		stats.StatusCounts[status] = count
		stats.Total += count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("status counts: %w", err)
	}

	if err := s.pool.QueryRow(ctx, "SELECT COALESCE(SUM(item_count), 0)::int FROM restaurants").Scan(&stats.TotalItems); err != nil {
		return nil, fmt.Errorf("total items: %w", err)
	}

	tierRows, err := s.pool.Query(ctx, tierCountsSQL)
	if err != nil {
		return nil, fmt.Errorf("tier counts: %w", err)
	}
	defer tierRows.Close()
	for tierRows.Next() {
		var tier string
		var stat server.TierStat
		if err := tierRows.Scan(&tier, &stat.Count, &stat.Items); err != nil {
			return nil, fmt.Errorf("scan tier counts: %w", err)
		}
		stats.TierCounts[tier] = stat
	}
	if err := tierRows.Err(); err != nil {
		return nil, fmt.Errorf("tier counts: %w", err)
	}

	failRows, err := s.pool.Query(ctx, failureCountsSQL)
	if err != nil {
		return nil, fmt.Errorf("failure counts: %w", err)
	}
	defer failRows.Close()
	for failRows.Next() {
		var reason string
		var count int
		if err := failRows.Scan(&reason, &count); err != nil {
			return nil, fmt.Errorf("scan failure counts: %w", err)
		}
		stats.FailureCounts[reason] = count
	}
	if err := failRows.Err(); err != nil {
		return nil, fmt.Errorf("failure counts: %w", err)
	}

	if err := s.jobCounts(ctx, stats.JobCounts); err != nil {
		slog.Warn("menusearch: river job counts unavailable", "schema", s.riverSchema, "err", err)
	}
	return stats, nil
}

// jobCounts fills counts with River job totals by kind and state, querying
// the river_job table in the configured schema.
func (s *Store) jobCounts(ctx context.Context, counts map[string]map[string]int) error {
	table := pgx.Identifier{s.riverSchema, "river_job"}.Sanitize()
	rows, err := s.pool.Query(ctx, fmt.Sprintf(jobCountsSQL, table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, state string
		var count int
		if err := rows.Scan(&kind, &state, &count); err != nil {
			return err
		}
		if counts[kind] == nil {
			counts[kind] = make(map[string]int)
		}
		counts[kind][state] = count
	}
	return rows.Err()
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
