// Package scraper provides a restaurant menu scraping agent.
//
// The agent is built on top of Eino (github.com/cloudwego/eino) and uses a
// compose.Graph to wire together the pipeline stages:
//
//	OSM Discovery → Page Fetch → Content Route → Extract/Vision/PDF → FODMAP Analyze → Store
//
// Each stage is implemented as an Eino InvokableTool so that the Gemini
// ChatModel can also call them autonomously when given a free-form user query.
package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Config holds all settings for the scraping agent.
type Config struct {
	// OllamaURL is the base URL for the local Ollama server.
	// Defaults to "http://localhost:11434".
	OllamaURL string

	// VisionModel is the Ollama vision model to use for menu transcription.
	// Defaults to "gemma3".
	VisionModel string

	// ScraperDBPath is the SQLite database path for the restaurant/menu cache.
	// Defaults to "scraper.db".
	ScraperDBPath string

	// DefaultArea is the OpenStreetMap area name used for restaurant discovery.
	// Defaults to "New York City".
	DefaultArea string

	// MaxFetchRetries controls how many times the fetcher retries a URL.
	MaxFetchRetries int

	// Logger is optional; defaults to slog.Default().
	Logger *slog.Logger
}

func (c *Config) applyDefaults() {
	if c.OllamaURL == "" {
		c.OllamaURL = "http://localhost:11434"
	}
	if c.VisionModel == "" {
		c.VisionModel = "gemma3"
	}
	if c.ScraperDBPath == "" {
		c.ScraperDBPath = "scraper.db"
	}
	if c.DefaultArea == "" {
		c.DefaultArea = "New York City"
	}
	if c.MaxFetchRetries <= 0 {
		c.MaxFetchRetries = 3
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Agent is the top-level scraping orchestrator.
//
// For now it runs a deterministic pipeline. After the Eino dependency is
// installed (go get github.com/cloudwego/eino), the pipeline will be migrated
// to a compose.Graph so that the Gemini ChatModel can also drive it as a tool.
type Agent struct {
	cfg       Config
	discovery *DiscoveryClient
	fetcher   *Fetcher
	extractor *Extractor
	vision    *VisionTranscriber
	analyzer  *Analyzer
	store     *Store
}

// NewAgent creates a new Agent with the given configuration.
// It opens (or creates) the scraper SQLite database.
func NewAgent(cfg Config) (*Agent, error) {
	cfg.applyDefaults()

	store, err := NewStore(cfg.ScraperDBPath)
	if err != nil {
		return nil, fmt.Errorf("opening scraper store: %w", err)
	}

	return &Agent{
		cfg:       cfg,
		discovery: NewDiscoveryClient(cfg.Logger),
		fetcher:   NewFetcher(cfg.Logger),
		extractor: NewExtractor(3, cfg.Logger),
		vision:    NewVisionTranscriber(cfg.OllamaURL, cfg.VisionModel, cfg.Logger),
		analyzer:  NewAnalyzer(),
		store:     store,
	}, nil
}

// Close releases resources held by the agent.
func (a *Agent) Close() error { return a.store.Close() }

// Scrape runs the full pipeline for the given ScrapeRequest and returns a ScrapeResult.
//
// Pipeline:
//  1. Resolve restaurant URL (from request or OSM lookup).
//  2. Discover the menu page URL from the homepage.
//  3. Fetch page content (Tier 1 HTTP, fallback Tier 2 chromedp).
//  4. Extract menu items (HTML extractor, PDF, or vision fallback).
//  5. Optionally run FODMAP analysis.
//  6. Persist results to SQLite.
func (a *Agent) Scrape(ctx context.Context, req ScrapeRequest) (*ScrapeResult, error) {
	log := a.cfg.Logger
	startedAt := time.Now()

	// ---- Step 1: Resolve URL ----
	targetURL := req.URL
	businessName := req.BusinessName
	var osmID int64

	if targetURL == "" && businessName != "" {
		area := req.City
		if area == "" {
			area = a.cfg.DefaultArea
		}
		log.Info("looking up restaurant via OSM", "name", businessName, "area", area)
		restaurant, err := a.discovery.FindRestaurant(ctx, businessName, area)
		if err != nil {
			return nil, fmt.Errorf("OSM lookup: %w", err)
		}
		if restaurant == nil {
			return nil, fmt.Errorf("restaurant %q not found in OpenStreetMap for area %q", businessName, area)
		}
		osmID = restaurant.ID
		targetURL = restaurant.Website()
		if targetURL == "" {
			return nil, fmt.Errorf("no website found for %q in OpenStreetMap — provide a URL directly", businessName)
		}
		if businessName == "" {
			businessName = restaurant.Name()
		}
		log.Info("resolved restaurant", "name", businessName, "url", targetURL)
	}

	if targetURL == "" {
		return nil, fmt.Errorf("provide either a URL or a business name")
	}

	// ---- Step 2: Discover menu page ----
	menuURL, err := a.fetcher.DiscoverMenuURL(ctx, targetURL)
	if err != nil {
		log.Warn("menu URL discovery failed, using root URL", "url", targetURL, "error", err)
		menuURL = targetURL
	}
	log.Info("fetching menu page", "url", menuURL)

	// ---- Step 3: Fetch ----
	fetchResult, err := a.fetcher.Fetch(ctx, menuURL)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", menuURL, err)
	}

	// ---- Step 4: Extract ----
	var items []MenuItem
	source := fetchResult.ContentType

	switch fetchResult.ContentType {
	case "html":
		items, err = a.extractHTML(ctx, fetchResult)
	case "pdf":
		items, err = a.extractPDF(ctx, fetchResult)
	case "image":
		items, err = a.vision.TranscribeImage(ctx, fetchResult.Body)
		source = "vision"
	default:
		items, err = a.extractHTML(ctx, fetchResult)
	}

	if err != nil {
		return nil, fmt.Errorf("extraction failed: %w", err)
	}

	result := &ScrapeResult{
		BusinessName: businessName,
		MenuURL:      menuURL,
		Items:        items,
		Source:       source,
		ScrapedAt:    startedAt,
	}

	// ---- Step 5: FODMAP analysis ----
	if req.Analyze && len(items) > 0 {
		analysis := a.analyzer.Analyze(ctx, businessName, menuURL, items)
		result.Analysis = analysis
	}

	// ---- Step 6: Persist ----
	if _, err := a.store.SaveMenu(ctx, osmID, result); err != nil {
		// Non-fatal: log and continue.
		log.Warn("failed to persist menu to SQLite", "error", err)
		result.Warnings = append(result.Warnings, "storage: "+err.Error())
	}

	log.Info("scrape complete",
		"restaurant", businessName,
		"items", len(items),
		"source", source,
		"elapsed", time.Since(startedAt).Round(time.Millisecond),
	)
	return result, nil
}

// DiscoverNYC bulk-discovers all restaurants in New York City via OpenStreetMap
// and caches them to the local SQLite database. This is a one-time seeding
// operation; subsequent searches use the local cache.
func (a *Agent) DiscoverNYC(ctx context.Context) (int, error) {
	restaurants, err := a.discovery.DiscoverRestaurants(ctx, a.cfg.DefaultArea)
	if err != nil {
		return 0, err
	}
	if err := a.store.UpsertRestaurants(ctx, restaurants); err != nil {
		return 0, fmt.Errorf("caching restaurants: %w", err)
	}
	return len(restaurants), nil
}

// SearchCached searches the local SQLite restaurant cache for a restaurant name.
func (a *Agent) SearchCached(ctx context.Context, query string) ([]OSMRestaurant, error) {
	return a.store.SearchRestaurants(ctx, query)
}

// ---- internal extraction helpers ----

// extractHTML tries HTML extraction, falling back to Ollama vision if needed.
func (a *Agent) extractHTML(ctx context.Context, fetch *FetchResult) ([]MenuItem, error) {
	items, err := a.extractor.Extract(ctx, fetch.Body)
	if err != nil {
		return nil, err
	}
	if len(items) >= 3 {
		return items, nil
	}
	// Too few items from HTML extraction — try vision on the raw HTML text.
	a.cfg.Logger.Info("HTML extraction insufficient, falling back to Ollama text transcription",
		"items_found", len(items))
	rawText := stripHTML(fetch.Body)
	return a.vision.TranscribeText(ctx, rawText)
}

// extractPDF extracts text from a PDF and sends it to Ollama for structuring.
// For now, falls back directly to Ollama text transcription since pdfcpu
// integration is in the next phase.
func (a *Agent) extractPDF(ctx context.Context, fetch *FetchResult) ([]MenuItem, error) {
	// TODO Phase 1d: integrate pdfcpu for text layer extraction.
	// For now, treat PDF bytes as opaque and send to vision model.
	a.cfg.Logger.Info("PDF detected — sending to Ollama vision", "bytes", len(fetch.Body))
	return a.vision.TranscribeImage(ctx, fetch.Body)
}

// stripHTML removes HTML tags from body bytes and returns clean text.
func stripHTML(body []byte) string {
	s := string(body)
	var sb strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			sb.WriteRune(' ')
		case !inTag:
			sb.WriteRune(r)
		}
	}
	// Collapse whitespace.
	result := sb.String()
	for strings.Contains(result, "  ") {
		result = strings.ReplaceAll(result, "  ", " ")
	}
	return strings.TrimSpace(result)
}
