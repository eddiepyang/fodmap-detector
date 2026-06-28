package server

import (
	"context"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// Restaurant represents a row in the restaurants table.
type Restaurant struct {
	CAMIS         string     `json:"camis"`
	DBA           string     `json:"dba"`
	Boro          *string    `json:"boro"`
	Building      *string    `json:"building"`
	Street        *string    `json:"street"`
	Zipcode       *string    `json:"zipcode"`
	Phone         *string    `json:"phone"`
	Cuisine       *string    `json:"cuisine"`
	Latitude      *float64   `json:"latitude"`
	Longitude     *float64   `json:"longitude"`
	NTA           *string    `json:"nta"`
	Status        string     `json:"status"`
	MenuURL       *string    `json:"menu_url"`
	MenuURLSource *string    `json:"menu_url_source"`
	ItemCount     int        `json:"item_count"`
	ScrapedAt     *time.Time `json:"scraped_at"`
	LastError     *string    `json:"last_error"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// RestaurantStore manages the restaurants table. Implemented by
// menusearch.store; defined here so server handlers don't import menusearch.
type RestaurantStore interface {
	Upsert(ctx context.Context, r Restaurant) error
	Get(ctx context.Context, camis string) (*Restaurant, error)
	List(ctx context.Context, status string, search string, limit, offset int) ([]Restaurant, error)
	UpdateMenuURL(ctx context.Context, camis, menuURL, source string) error
	UpdateScrapeResult(ctx context.Context, camis, status string, itemCount int, lastError string) error
}

// RiverInserter inserts River jobs. Same interface as menutracking's.
type RiverInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}
