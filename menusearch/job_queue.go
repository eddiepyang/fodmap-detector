package menusearch

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"fodmap/server"
)

// JobQueue implements server.RestaurantJobQueue using the River client.
type JobQueue struct {
	Client      *river.Client[pgx.Tx]
	MaxAttempts int
}

// EnqueueDiscover inserts a discover_menu_url job for the given restaurant.
// Returns server.ErrJobAlreadyQueued (wrapped) if River deduplication prevents it.
func (q *JobQueue) EnqueueDiscover(ctx context.Context, r server.Restaurant) error {
	args := DiscoverMenuURLArgs{
		CAMIS:    safeDeref(r.CAMIS),
		DBA:      r.DBA,
		Building: safeDeref(r.Building),
		Street:   safeDeref(r.Street),
		Boro:     safeDeref(r.Boro),
		Zipcode:  safeDeref(r.Zipcode),
		Attempt:  1,
	}
	opts := &river.InsertOpts{
		MaxAttempts: q.MaxAttempts,
		UniqueOpts: river.UniqueOpts{
			ByArgs:   true,
			ByPeriod: 30 * 24 * time.Hour,
		},
	}
	res, err := q.Client.Insert(ctx, args, opts)
	if err != nil {
		return fmt.Errorf("enqueue discover: %w", err)
	}
	if res.UniqueSkippedAsDuplicate {
		return server.ErrJobAlreadyQueued
	}
	return nil
}

// EnqueueScrape inserts a scrape_menu job for the given restaurant.
// The restaurant must have a non-empty MenuURL.
func (q *JobQueue) EnqueueScrape(ctx context.Context, r server.Restaurant) error {
	if len(r.MenuURLs) == 0 {
		return fmt.Errorf("restaurant %s has no menu_urls", safeDeref(r.CAMIS))
	}
	allSkipped := true
	for _, u := range r.MenuURLs {
		args := ScrapeMenuArgs{
			RestaurantID: r.ID,
			URL:          u,
			DBA:          r.DBA,
		}
		opts := &river.InsertOpts{
			MaxAttempts: q.MaxAttempts,
			UniqueOpts: river.UniqueOpts{
				ByArgs:   true,
				ByPeriod: 30 * 24 * time.Hour,
			},
		}
		res, err := q.Client.Insert(ctx, args, opts)
		if err != nil {
			return fmt.Errorf("enqueue scrape %s: %w", u, err)
		}
		if !res.UniqueSkippedAsDuplicate {
			allSkipped = false
		}
	}
	if allSkipped {
		return server.ErrJobAlreadyQueued
	}
	return nil
}

func safeDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// compile-time check
var _ server.RestaurantJobQueue = (*JobQueue)(nil)
