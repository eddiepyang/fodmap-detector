// Package scraper provides a restaurant menu scraping agent powered by Eino,
// OpenStreetMap, Colly, chromedp, and Ollama vision models.
package scraper

import "time"

// ---- Menu data types ----

// MenuItem represents a single dish or drink on a restaurant menu.
type MenuItem struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Price       string   `json:"price,omitempty"`
	Category    string   `json:"category,omitempty"` // "appetizers", "entrees", "drinks", etc.
	Ingredients []string `json:"ingredients,omitempty"`
}

// FodmapFlag records a single FODMAP concern on a menu item.
type FodmapFlag struct {
	Ingredient string `json:"ingredient"`
	Level      string `json:"level"` // "high", "moderate"
	Notes      string `json:"notes,omitempty"`
}

// AnalyzedMenuItem is a MenuItem enriched with FODMAP analysis.
type AnalyzedMenuItem struct {
	MenuItem
	FodmapFlags []FodmapFlag `json:"fodmap_flags,omitempty"`
	SafetyScore string       `json:"safety_score"` // "safe", "caution", "avoid"
}

// MenuAnalysis is the full FODMAP report for a restaurant's menu.
type MenuAnalysis struct {
	BusinessName string             `json:"business_name"`
	MenuURL      string             `json:"menu_url"`
	Items        []AnalyzedMenuItem `json:"items"`
	Summary      string             `json:"summary"`
	ScrapedAt    time.Time          `json:"scraped_at"`
}

// ---- Request / response types ----

// ScrapeRequest describes what to scrape.
type ScrapeRequest struct {
	// URL is a direct link to a restaurant homepage or menu page.
	// If empty, the agent will look up the restaurant by name via OpenStreetMap.
	URL string `json:"url,omitempty"`

	// BusinessName is used for OSM lookup when URL is not provided.
	BusinessName string `json:"business_name,omitempty"`

	// City/State narrow the OSM search. Defaults to "New York City".
	City  string `json:"city,omitempty"`
	State string `json:"state,omitempty"`

	// Analyze triggers FODMAP cross-referencing after extraction.
	Analyze bool `json:"analyze,omitempty"`
}

// ScrapeResult is the output of a successful scrape run.
type ScrapeResult struct {
	BusinessName string      `json:"business_name"`
	MenuURL      string      `json:"menu_url"`
	Items        []MenuItem  `json:"items"`
	Analysis     *MenuAnalysis `json:"analysis,omitempty"`
	Source       string      `json:"source"` // "html", "pdf", "vision", "cached"
	ScrapedAt    time.Time   `json:"scraped_at"`

	// Errors accumulated during the run (non-fatal).
	Warnings []string `json:"warnings,omitempty"`
}

// ---- OpenStreetMap types ----

// OSMRestaurant is a restaurant entry returned by the Overpass API.
type OSMRestaurant struct {
	ID   int64              `json:"id"`
	Type string             `json:"type"` // "node", "way", "relation"
	Lat  float64            `json:"lat"`
	Lon  float64            `json:"lon"`
	Tags map[string]string  `json:"tags"`
}

// Name returns the restaurant's name from OSM tags.
func (r *OSMRestaurant) Name() string { return r.Tags["name"] }

// Cuisine returns the cuisine type (may be empty).
func (r *OSMRestaurant) Cuisine() string { return r.Tags["cuisine"] }

// Website returns the website URL (may be empty).
func (r *OSMRestaurant) Website() string { return r.Tags["website"] }

// Phone returns the phone number (may be empty).
func (r *OSMRestaurant) Phone() string { return r.Tags["phone"] }

// Address constructs a human-readable address from OSM tags.
func (r *OSMRestaurant) Address() string {
	parts := []string{}
	if n := r.Tags["addr:housenumber"]; n != "" {
		parts = append(parts, n)
	}
	if s := r.Tags["addr:street"]; s != "" {
		parts = append(parts, s)
	}
	if c := r.Tags["addr:city"]; c != "" {
		parts = append(parts, c)
	} else {
		// Default area if city tag missing
		parts = append(parts, "New York")
	}
	if len(parts) == 0 {
		return ""
	}
	addr := ""
	for i, p := range parts {
		if i == 0 {
			addr = p
		} else if i == len(parts)-1 {
			addr += ", " + p
		} else {
			addr += " " + p
		}
	}
	return addr
}
