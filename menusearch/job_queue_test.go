package menusearch

import (
	"context"
	"errors"
	"testing"

	"fodmap/server"
)

func TestJobQueue_EnqueueDiscover_Disabled(t *testing.T) {
	q := &JobQueue{DiscoverEnabled: false}
	err := q.EnqueueDiscover(context.Background(), server.Restaurant{})
	if !errors.Is(err, server.ErrJobKindNotRegistered) {
		t.Fatalf("err = %v, want ErrJobKindNotRegistered", err)
	}
}

func TestJobQueue_EnqueueScrape_Disabled(t *testing.T) {
	q := &JobQueue{ScrapeEnabled: false}
	err := q.EnqueueScrape(context.Background(), server.Restaurant{MenuURLs: []string{"https://example.com/menu"}})
	if !errors.Is(err, server.ErrJobKindNotRegistered) {
		t.Fatalf("err = %v, want ErrJobKindNotRegistered", err)
	}
}
