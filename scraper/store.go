package scraper

import "context"

// Store persists scraped restaurant and menu data.
type Store interface {
	Close() error
	UpsertRestaurants(ctx context.Context, restaurants []OSMRestaurant) error
	SearchRestaurants(ctx context.Context, query string) ([]OSMRestaurant, error)
	SaveMenu(ctx context.Context, osmID int64, result *ScrapeResult) (string, error)
	SaveAnalysis(ctx context.Context, menuID string, analysis *MenuAnalysis) error
}
