package menusearch

import (
	"testing"

	"github.com/google/uuid"
)

func TestDiscoverMenuURLArgs_Kind(t *testing.T) {
	args := DiscoverMenuURLArgs{}
	if got := args.Kind(); got != "menusearch.discover_menu_url" {
		t.Errorf("Kind() = %q, want %q", got, "menusearch.discover_menu_url")
	}
}

func TestScrapeMenuArgs_Kind(t *testing.T) {
	args := ScrapeMenuArgs{}
	if got := args.Kind(); got != "menusearch.scrape_menu" {
		t.Errorf("Kind() = %q, want %q", got, "menusearch.scrape_menu")
	}
}

func TestScrapeMenuArgs_HasDiscoveryEventID(t *testing.T) {
	args := ScrapeMenuArgs{
		RestaurantID:     uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		URL:              "https://example.com/menu",
		DBA:              "Test Restaurant",
		DiscoveryEventID: "evt-abc-123",
	}
	if args.DiscoveryEventID != "evt-abc-123" {
		t.Errorf("DiscoveryEventID = %q, want %q", args.DiscoveryEventID, "evt-abc-123")
	}
}
