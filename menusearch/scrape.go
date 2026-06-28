package menusearch

import (
	"context"
	"fmt"
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
		_ = w.Store.UpdateScrapeResult(ctx, args.CAMIS, StatusFailedScrape, 0, err.Error())
		return fmt.Errorf("extract menu: %w", err)
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

	if result == nil || len(result.Items) == 0 {
		_ = w.Store.UpdateScrapeResult(ctx, args.CAMIS, StatusFailedScrape, 0, "no menu items found")
		return fmt.Errorf("no menu items found")
	}

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

	record := MenuExtractionRecord{
		CAMIS:            args.CAMIS,
		SourceURL:        args.URL,
		RestaurantName:   result.RestaurantName,
		Items:            items,
		EventID:          eventID,
		JobID:            fmt.Sprintf("%d", job.ID),
		Attempt:          job.Attempt,
		DiscoveryEventID: args.DiscoveryEventID,
	}

	avroDest := filepath.Join(w.AvroDestDir, fmt.Sprintf("%s-%d.avro", args.CAMIS, job.Attempt))
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

	logger.Info("scrape successful", "count", count)
	return nil
}
