package server

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// Restaurant represents a row in the restaurants table.
type Restaurant struct {
	ID             uuid.UUID  `json:"id"`
	CAMIS          *string    `json:"camis,omitempty"`
	YelpID         *string    `json:"yelp_id,omitempty"`
	DBA            string     `json:"dba"`
	Boro           *string    `json:"boro"`
	Building       *string    `json:"building"`
	Street         *string    `json:"street"`
	Zipcode        *string    `json:"zipcode"`
	Phone          *string    `json:"phone"`
	Address        *string    `json:"address"`
	Cuisine        *string    `json:"cuisine"`
	Latitude       *float64   `json:"latitude"`
	Longitude      *float64   `json:"longitude"`
	NTA            *string    `json:"nta"`
	Status         string     `json:"status"`
	WebsiteURL     *string    `json:"website_url"`
	MenuURLs       []string   `json:"menu_urls"`
	URLSource      *string    `json:"url_source"`
	ExtractionTier *string    `json:"extraction_tier"`
	ItemCount      int        `json:"item_count"`
	ScrapedAt      *time.Time `json:"scraped_at"`
	LastError      *string    `json:"last_error"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// TierStat aggregates scraped restaurants by extraction tier.
type TierStat struct {
	Count int `json:"count"`
	Items int `json:"items"`
}

// PipelineStats is the aggregate rollup powering the admin scraper-pipeline
// page: restaurant counts by status, extraction-tier mix, a coarse failure
// taxonomy bucketed from last_error, and River job counts by kind and state.
type PipelineStats struct {
	Total         int                       `json:"total"`
	TotalItems    int                       `json:"total_items"`
	StatusCounts  map[string]int            `json:"status_counts"`
	TierCounts    map[string]TierStat       `json:"tier_counts"`
	FailureCounts map[string]int            `json:"failure_counts"`
	JobCounts     map[string]map[string]int `json:"job_counts"`
}

// RestaurantStore manages the restaurants table. Implemented by
// menusearch.store; defined here so server handlers don't import menusearch.
type RestaurantStore interface {
	Upsert(ctx context.Context, r Restaurant) (*Restaurant, error)
	Get(ctx context.Context, camis string) (*Restaurant, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Restaurant, error)
	List(ctx context.Context, status string, search string, limit, offset int) ([]Restaurant, error)
	Count(ctx context.Context, status string, search string) (int, error)
	PipelineStats(ctx context.Context) (*PipelineStats, error)
	UpdateDiscoveryURLs(ctx context.Context, camis, websiteURL string, menuURLs []string, source, address, phone string) error
	UpdateScrapeResult(ctx context.Context, camis, status string, itemCount int, lastError string) error
}

// RiverInserter inserts River jobs. Same interface as menutracking's.
type RiverInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// RestaurantJobQueue enqueues discovery and scrape jobs for a restaurant.
// Implemented by menusearch; defined here so server handlers don't import menusearch.
type RestaurantJobQueue interface {
	EnqueueDiscover(ctx context.Context, r Restaurant) error
	EnqueueScrape(ctx context.Context, r Restaurant) error
}

// ErrJobAlreadyQueued is returned by RestaurantJobQueue methods when River
// deduplication prevents inserting a duplicate job within the uniqueness window.
var ErrJobAlreadyQueued = errors.New("job already queued")

// ErrJobKindNotRegistered is returned by RestaurantJobQueue methods when the
// worker for the requested job kind is not registered in the running pipeline.
var ErrJobKindNotRegistered = errors.New("job kind not registered in this pipeline")
