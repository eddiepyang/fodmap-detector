package menusearch

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"fodmap/pipeline"
	"fodmap/scraper"
	"fodmap/search"
	"fodmap/server"
)

type ScrapeMenuWorker struct {
	river.WorkerDefaults[ScrapeMenuArgs]
	Store           *Store
	MenuStore       server.MenuStore
	Embedder        search.Embedder
	Fetcher         scraper.Fetcher
	Extractor       scraper.Extractor
	AvroDestDir     string
	EnableVision    bool
	UsePdftotext    bool
	WebagentAdapter string // "site/target" passed to ServiceExtractor.ScrapeJS
	BronzeDir       string // base dir for raw HTML; defaults to data/bronze/restaurants
}

func (w *ScrapeMenuWorker) bronzeDir() string {
	if w.BronzeDir != "" {
		return w.BronzeDir
	}
	if envDir := os.Getenv("RESTAURANT_BRONZE_DIR"); envDir != "" {
		return envDir
	}
	return "data/bronze/restaurants"
}

func (w *ScrapeMenuWorker) Timeout(job *river.Job[ScrapeMenuArgs]) time.Duration {
	return 5 * time.Minute
}

func (w *ScrapeMenuWorker) Work(ctx context.Context, job *river.Job[ScrapeMenuArgs]) error {
	args := job.Args
	logger := slog.With("job", job.ID, "camis", args.CAMIS, "url", args.URL)
	logger.Info("starting scrape job")

	err := w.Store.UpdateScrapeResult(ctx, args.CAMIS, StatusScraping, 0, "")
	if err != nil {
		return fmt.Errorf("update status to scraping: %w", err)
	}

	result, rawBody, err := pipeline.ExtractMenu(ctx, args.URL, w.Fetcher, w.Extractor, w.EnableVision, w.UsePdftotext, w.WebagentAdapter)
	if err != nil {
		// Transient render errors (503 BrowserBusy/WafBlocked, 504 FetchTimeout)
		// must not clobber the restaurant status — River will retry the job up to
		// MaxAttempts times. Write StatusFailedScrape only for permanent failures.
		if !scraper.IsRenderTransient(err) {
			_ = w.Store.UpdateScrapeResult(ctx, args.CAMIS, StatusFailedScrape, 0, err.Error())
		} else {
			logger.Warn("transient render error; skipping failed_scrape write for retry", "error", err)
		}
		return fmt.Errorf("extract menu: %w", err)
	}

	rest, err := w.Store.Get(ctx, args.CAMIS)
	if err != nil {
		logger.Warn("failed to get restaurant for address enrichment", "error", err)
	} else if rest != nil {
		if rest.Address != nil {
			result.Address = *rest.Address
		}
		if rest.Phone != nil {
			result.PhoneNumber = *rest.Phone
		}
	}

	// Write raw body to bronze layer (best-effort). PDF bytes use .html extension
	// like menutracking; the extension is informational only.
	if len(rawBody) > 0 {
		date := time.Now().UTC().Format("2006-01-02")
		htmlPath := filepath.Join(w.bronzeDir(), date, fmt.Sprintf("%s-%d.html", args.CAMIS, job.Attempt))
		if mkErr := os.MkdirAll(filepath.Dir(htmlPath), 0o755); mkErr == nil {
			if wErr := os.WriteFile(htmlPath, rawBody, 0o644); wErr != nil {
				logger.Warn("failed to write HTML bronze", "path", htmlPath, "error", wErr)
			}
		}
	}

	// ── Directory / paginated-menu expansion ────────────────────────────────
	// When the root URL yields no items and this is a depth-0 job, attempt to
	// extract candidate sub-URLs from the page HTML, validate them, fetch each
	// through the full cascade, and aggregate the results into a single write.
	// Sub-URL fetches happen at depth 1 (in-loop) and never recurse further.
	if (result == nil || len(result.Items) == 0) && args.Depth == 0 {
		expanded, expandErr := w.tryDirectoryExpansion(ctx, args, rawBody, result, logger)
		if expandErr != nil {
			// tryDirectoryExpansion already wrote failed_scrape when appropriate.
			return expandErr
		}
		if expanded {
			// The expansion path handled its own StoreMenu + UpdateScrapeResult.
			return nil
		}
		// No expansion possible (no candidates or all failed) — fall through to
		// the standard failed_scrape write below.
	}

	if result == nil || len(result.Items) == 0 {
		_ = w.Store.UpdateScrapeResult(ctx, args.CAMIS, StatusFailedScrape, 0, "no menu items found")
		return fmt.Errorf("no menu items found")
	}

	return w.storeAndFinish(ctx, job, args, result, logger)
}

// tryDirectoryExpansion attempts sub-URL discovery and extraction when the root
// URL produced no menu items.  It returns (true, nil) when expansion succeeded
// and the job is complete, (false, nil) when no candidates were found or all
// failed (caller should write failed_scrape), and (false, err) on a hard error.
func (w *ScrapeMenuWorker) tryDirectoryExpansion(
	ctx context.Context,
	args ScrapeMenuArgs,
	rawBody []byte,
	rootResult *scraper.MenuExtractionResult,
	logger *slog.Logger,
) (bool, error) {
	logger.Info(fmt.Sprintf("directory expansion of %s: root URL %s yielded 0 items; attempting sub-URL extraction", args.CAMIS, args.URL))

	// Obtain HTML for anchor parsing.  rawBody is populated by the normal fetch
	// path.  For JS-rendered pages the pipeline returns nil rawBody — in that
	// case we call FetchRenderedHTML if the extractor supports it.
	html := rawBody
	if len(html) == 0 {
		renderer, ok := w.Extractor.(scraper.HTMLRenderer)
		if !ok {
			logger.Info("directory expansion: no rendered HTML available and extractor is not an HTMLRenderer; skipping expansion")
			return false, nil
		}
		renderRes, renderErr := renderer.FetchRenderedHTML(ctx, args.URL, scraper.RenderOptions{})
		if renderErr != nil {
			logger.Warn("directory expansion: FetchRenderedHTML failed; skipping expansion", "error", renderErr)
			return false, nil
		}
		body, readErr := io.ReadAll(renderRes.Body)
		_ = renderRes.Body.Close()
		if readErr != nil {
			logger.Warn("directory expansion: reading rendered HTML failed; skipping expansion", "error", readErr)
			return false, nil
		}
		html = body
	}

	candidates := extractMenuSubURLs(html, args.URL)
	if len(candidates) == 0 {
		logger.Info("directory expansion: no candidate sub-URLs found")
		return false, nil
	}
	logger.Info("directory expansion: candidate sub-URLs before signal filter", "count", len(candidates), "urls", candidates)

	// Validate candidates with a plain-GET menu-signal check (same pattern as
	// DiscoverMenuURLWorker).  Pass "" as primaryURL so no URL is pinned.
	httpClient := buildDirectoryClient()
	confirmed := menuSignalFilter(ctx, httpClient, candidates, "", logger)
	if len(confirmed) == 0 {
		logger.Info("directory expansion: no candidates survived signal filter")
		return false, nil
	}
	logger.Info("directory expansion: fetching sub-URLs", "count", len(confirmed))

	subResults := extractSubURLs(ctx, confirmed, w.Fetcher, w.Extractor,
		w.EnableVision, w.UsePdftotext, w.WebagentAdapter, logger)

	if len(subResults) == 0 {
		logger.Info("directory expansion: all sub-URLs yielded 0 items")
		return false, nil
	}

	// Aggregate items from all successful sub-URLs into one MenuExtractionResult.
	aggregated := scraper.MenuExtractionResult{
		ExtractionTier: pipeline.TierDirectoryFanout,
		SourceURL:      args.URL,
	}
	// Seed name / address from the root result if available.
	if rootResult != nil {
		aggregated.RestaurantName = rootResult.RestaurantName
		aggregated.City = rootResult.City
		aggregated.State = rootResult.State
		aggregated.Address = rootResult.Address
		aggregated.PhoneNumber = rootResult.PhoneNumber
	}
	for _, sr := range subResults {
		aggregated.Items = append(aggregated.Items, sr.result.Items...)
		// Fill in restaurant metadata from first sub-URL that provides it.
		if aggregated.RestaurantName == "" && sr.result.RestaurantName != "" {
			aggregated.RestaurantName = sr.result.RestaurantName
		}
		if aggregated.City == "" && sr.result.City != "" {
			aggregated.City = sr.result.City
			aggregated.State = sr.result.State
		}
		if aggregated.Address == "" && sr.result.Address != "" {
			aggregated.Address = sr.result.Address
		}
		if aggregated.PhoneNumber == "" && sr.result.PhoneNumber != "" {
			aggregated.PhoneNumber = sr.result.PhoneNumber
		}
	}
	aggregated.ScrapedAtUTC = time.Now().UTC().Format(time.RFC3339)

	// Write sub-URL raw bodies to bronze (best-effort, per-URL provenance).
	for i, sr := range subResults {
		if len(sr.rawBody) == 0 {
			continue
		}
		date := time.Now().UTC().Format("2006-01-02")
		subPath := filepath.Join(w.bronzeDir(), date,
			fmt.Sprintf("%s-sub%d.html", args.CAMIS, i))
		if mkErr := os.MkdirAll(filepath.Dir(subPath), 0o755); mkErr == nil {
			if wErr := os.WriteFile(subPath, sr.rawBody, 0o644); wErr != nil {
				logger.Warn("failed to write sub-URL bronze", "path", subPath, "error", wErr)
			}
		}
	}

	logger.Info("directory expansion: aggregated items", "count", len(aggregated.Items), "sub_urls", len(subResults))

	if err := w.storeAndFinish(ctx, nil, args, &aggregated, logger); err != nil {
		return false, err
	}
	return true, nil
}

// storeAndFinish writes the Avro record, stores the menu items, updates scrape
// status, and records the extraction tier.  job may be nil when called from the
// directory expansion path (attempt/jobID fields are left as zero/empty).
func (w *ScrapeMenuWorker) storeAndFinish(
	ctx context.Context,
	job *river.Job[ScrapeMenuArgs],
	args ScrapeMenuArgs,
	result *scraper.MenuExtractionResult,
	logger *slog.Logger,
) error {
	eventID := uuid.NewString()
	items := make([]search.MenuItem, 0, len(result.Items))
	for _, entry := range result.Items {
		items = append(items, search.MenuItem{
			DishName:           entry.DishName,
			Description:        entry.Description,
			StatedIngredients:  entry.StatedIngredients,
			HasFullIngredients: entry.HasFullIngredients,
		})
	}

	var jobIDStr string
	var attempt int
	if job != nil {
		jobIDStr = fmt.Sprintf("%d", job.ID)
		attempt = job.Attempt
	}

	record := MenuExtractionRecord{
		CAMIS:            args.CAMIS,
		SourceURL:        args.URL,
		RestaurantName:   result.RestaurantName,
		Items:            items,
		EventID:          eventID,
		JobID:            jobIDStr,
		Attempt:          attempt,
		DiscoveryEventID: args.DiscoveryEventID,
		ExtractionTier:   result.ExtractionTier,
	}

	avroDest := filepath.Join(w.AvroDestDir, fmt.Sprintf("%s-%d.avro", args.CAMIS, attempt))
	if err := WriteMenuExtractionAvro(ctx, avroDest, record); err != nil {
		logger.Error("failed to write avro", "error", err)
	}

	count, err := pipeline.StoreMenu(ctx, result, args.URL, w.MenuStore, w.Embedder)
	if err != nil {
		_ = w.Store.UpdateScrapeResult(ctx, args.CAMIS, StatusFailedScrape, 0, err.Error())
		return fmt.Errorf("store menu: %w", err)
	}

	err = w.Store.UpdateScrapeResult(ctx, args.CAMIS, StatusScraped, count, "")
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	// Tier-mix telemetry (best-effort): record which cascade tier produced this
	// result so the JSON-LD vs. LLM/OCR/browser split can be measured.
	if tErr := w.Store.SetExtractionTier(ctx, args.CAMIS, result.ExtractionTier); tErr != nil {
		logger.Warn("failed to record extraction tier", "tier", result.ExtractionTier, "error", tErr)
	}

	logger.Info("scrape successful", "count", count, "tier", result.ExtractionTier)
	return nil
}
