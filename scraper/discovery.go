package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	overpassURL     = "https://overpass-api.de/api/interpreter"
	overpassTimeout = 90 * time.Second
)

// overpassResponse is the top-level JSON envelope from the Overpass API.
type overpassResponse struct {
	Elements []overpassElement `json:"elements"`
}

// overpassElement represents a node/way/relation from Overpass.
type overpassElement struct {
	Type   string            `json:"type"`
	ID     int64             `json:"id"`
	Lat    float64           `json:"lat"`
	Lon    float64           `json:"lon"`
	Center *overpassCenter   `json:"center,omitempty"` // for ways/relations
	Tags   map[string]string `json:"tags"`
}

type overpassCenter struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// DiscoveryClient queries the OpenStreetMap Overpass API for restaurants.
type DiscoveryClient struct {
	httpClient *http.Client
	logger     *slog.Logger
}

// NewDiscoveryClient creates a new DiscoveryClient.
func NewDiscoveryClient(logger *slog.Logger) *DiscoveryClient {
	if logger == nil {
		logger = slog.Default()
	}
	return &DiscoveryClient{
		httpClient: &http.Client{Timeout: overpassTimeout},
		logger:     logger,
	}
}

// DiscoverRestaurants queries the Overpass API for all restaurants in the
// named area (e.g. "New York City"). Returns a slice of OSMRestaurant.
//
// The caller should cache results to avoid hitting Overpass repeatedly.
func (d *DiscoveryClient) DiscoverRestaurants(ctx context.Context, area string) ([]OSMRestaurant, error) {
	if area == "" {
		area = "New York City"
	}

	// Overpass QL query — returns nodes, ways, and relations tagged amenity=restaurant.
	// "out center" makes ways/relations also return a lat/lon centroid.
	query := fmt.Sprintf(`[out:json][timeout:60];
area["name"="%s"]->.searchArea;
(
  node["amenity"="restaurant"](area.searchArea);
  way["amenity"="restaurant"](area.searchArea);
  relation["amenity"="restaurant"](area.searchArea);
);
out center;`, area)

	d.logger.Info("querying Overpass API", "area", area)
	start := time.Now()

	resp, err := d.httpClient.PostForm(overpassURL, url.Values{"data": {query}})
	if err != nil {
		return nil, fmt.Errorf("overpass request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("overpass returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result overpassResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding overpass response: %w", err)
	}

	restaurants := make([]OSMRestaurant, 0, len(result.Elements))
	for _, el := range result.Elements {
		r := OSMRestaurant{
			ID:   el.ID,
			Type: el.Type,
			Tags: el.Tags,
			Lat:  el.Lat,
			Lon:  el.Lon,
		}
		// Ways and relations return a centroid instead of direct lat/lon.
		if el.Center != nil {
			r.Lat = el.Center.Lat
			r.Lon = el.Center.Lon
		}
		// Skip unnamed entries — they're not useful.
		if r.Name() == "" {
			continue
		}
		restaurants = append(restaurants, r)
	}

	d.logger.Info("discovered restaurants",
		"area", area,
		"count", len(restaurants),
		"elapsed", time.Since(start).Round(time.Millisecond),
	)
	return restaurants, nil
}

// FindRestaurant searches OSM for a single restaurant by name in the given area.
// Returns the best match or nil if none found.
func (d *DiscoveryClient) FindRestaurant(ctx context.Context, name, area string) (*OSMRestaurant, error) {
	if area == "" {
		area = "New York City"
	}
	nameLower := strings.ToLower(name)

	// We search by name tag directly via Overpass.
	query := fmt.Sprintf(`[out:json][timeout:30];
area["name"="%s"]->.searchArea;
(
  node["amenity"="restaurant"]["name"~"%s",i](area.searchArea);
  way["amenity"="restaurant"]["name"~"%s",i](area.searchArea);
);
out center;`, area, name, name)

	resp, err := d.httpClient.PostForm(overpassURL, url.Values{"data": {query}})
	if err != nil {
		return nil, fmt.Errorf("overpass find request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result overpassResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding overpass find response: %w", err)
	}

	var best *OSMRestaurant
	for _, el := range result.Elements {
		r := OSMRestaurant{
			ID:   el.ID,
			Type: el.Type,
			Tags: el.Tags,
			Lat:  el.Lat,
			Lon:  el.Lon,
		}
		if el.Center != nil {
			r.Lat = el.Center.Lat
			r.Lon = el.Center.Lon
		}
		if strings.ToLower(r.Name()) == nameLower {
			// Exact match — prefer entries that have a website.
			if r.Website() != "" {
				return &r, nil
			}
			if best == nil {
				rCopy := r
				best = &rCopy
			}
		}
		// Partial match as fallback.
		if best == nil && strings.Contains(strings.ToLower(r.Name()), nameLower) {
			rCopy := r
			best = &rCopy
		}
	}
	return best, nil
}
