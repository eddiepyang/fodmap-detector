package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"fodmap/chat"
	"fodmap/menusearch"
	"fodmap/menutracking"
	menutrackingstore "fodmap/menutracking/store"
	"fodmap/scraper"
	"fodmap/search"
	"fodmap/server"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/genai"
)

var menutrackingCmd = &cobra.Command{
	Use:   "menutracking",
	Short: "Regulatory tracking pipeline commands.",
}

var menutrackingAddSourceCmd = &cobra.Command{
	Use:   "add-source <url>",
	Short: "Add a regulatory source to scrape periodically.",
	Args:  cobra.ExactArgs(1),
	RunE:  runMenutrackingAddSource,
}

func init() {
	rootCmd.AddCommand(menutrackingCmd)
	menutrackingCmd.AddCommand(menutrackingAddSourceCmd)

	menutrackingAddSourceCmd.Flags().String("name", "", "Human-readable name for the source")
	menutrackingAddSourceCmd.Flags().String("tier", "gov", "Source tier: gov | consultancy | commercial")
	menutrackingAddSourceCmd.Flags().String("cron", "@weekly", "Cron schedule: one of @daily, @hourly, @weekly")
	menutrackingAddSourceCmd.Flags().Int("max-tokens", 32000, "Max input tokens per source page")
}

func runMenutrackingAddSource(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dsn := viper.GetString("postgres-dsn")
	if dsn == "" {
		return fmt.Errorf("postgres-dsn is required (set via --postgres-dsn or POSTGRES_DSN env)")
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connecting to postgres: %w", err)
	}
	defer pool.Close()

	name, _ := cmd.Flags().GetString("name")
	tier, _ := cmd.Flags().GetString("tier")
	cron, _ := cmd.Flags().GetString("cron")
	maxTokens, _ := cmd.Flags().GetInt("max-tokens")
	rawURL := args[0]

	if _, err := parseCronSchedule(cron); err != nil {
		return err
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parsing url: %w", err)
	}
	domain := parsedURL.Hostname()

	src := &menutracking.Source{
		Name:         name,
		URL:          rawURL,
		Domain:       domain,
		Tier:         tier,
		CronSchedule: cron,
		MaxTokens:    maxTokens,
	}

	if err := menutracking.InsertSource(ctx, pool, src); err != nil {
		return fmt.Errorf("adding source: %w", err)
	}

	slog.Info("source added", "id", src.ID, "domain", src.Domain, "url", src.URL, "cron", src.CronSchedule)
	slog.Info("restart the daemon or send SIGHUP to pick up the new source")
	return nil
}

// PipelineConfig holds the configuration for starting the menutracking pipeline.
type PipelineConfig struct {
	DSN                       string
	Fetcher                   scraper.Fetcher
	VectorSink                menutracking.VectorSink
	ChatBackend               chat.ChatBackend
	MenuStore                 server.MenuStore
	Embedder                  search.Embedder
	GenAIClient               *genai.Client
	Extractor                 scraper.Extractor
	DiscoveryAvroDestDir      string
	DiscoveryGeminiModel      string
	DiscoveryStaggerSeconds   int
	DiscoveryMaxNoURLAttempts int
	ExtractionAvroDestDir     string
	EnableVision              bool
	UsePdftotext              bool
	WebagentAdapter           string
	BronzeDir                 string
	ScrapeMaxAttempts         int
}

// PipelineResult holds the running pipeline's stop function and references
// needed for runtime operations like source reloading.
type PipelineResult struct {
	Stop               func(context.Context) error
	Pool               *pgxpool.Pool
	RestaurantStore    server.RestaurantStore    // nil when menusearch not wired
	RestaurantJobQueue server.RestaurantJobQueue // nil when menusearch not wired
}

// deadLetterHandler implements river.ErrorHandler to persist discarded jobs
// to the menutracking_dead_letter table for long-term audit.
type deadLetterHandler struct {
	pool            *pgxpool.Pool
	restaurantStore *menusearch.Store // may be nil when menusearch not wired
}

func (h *deadLetterHandler) handleFinalScrapeFailure(ctx context.Context, job *rivertype.JobRow, errMsg string) {
	if h.restaurantStore == nil || job.Kind != "menusearch.scrape_menu" || job.Attempt < job.MaxAttempts {
		return
	}
	var args struct {
		CAMIS string `json:"camis"`
	}
	if jsonErr := json.Unmarshal(job.EncodedArgs, &args); jsonErr != nil || args.CAMIS == "" {
		return
	}
	if storeErr := h.restaurantStore.UpdateScrapeResult(ctx, args.CAMIS, menusearch.StatusFailedScrape, 0, errMsg); storeErr != nil {
		slog.Warn("menutracking: failed to update scrape status on discard", "camis", args.CAMIS, "err", storeErr)
	}
}

func (h *deadLetterHandler) HandleError(ctx context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	if dlErr := menutracking.DeadLetterHandler(ctx, h.pool, job.Kind, json.RawMessage(job.EncodedArgs), err.Error()); dlErr != nil {
		slog.Warn("menutracking: failed to write dead letter", "err", dlErr)
	}
	h.handleFinalScrapeFailure(ctx, job, err.Error())
	return nil
}

func (h *deadLetterHandler) HandlePanic(ctx context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	errMsg := fmt.Sprintf("panic: %v\n%s", panicVal, trace)
	if dlErr := menutracking.DeadLetterHandler(ctx, h.pool, job.Kind, json.RawMessage(job.EncodedArgs), errMsg); dlErr != nil {
		slog.Warn("menutracking: failed to write dead letter for panic", "err", dlErr)
	}
	h.handleFinalScrapeFailure(ctx, job, errMsg)
	return nil
}

// StartMenutrackingPipeline creates the pgxpool, river client, and workers, and
// starts the pipeline. It returns a stop function that drains in-flight jobs
// on shutdown. The caller is responsible for calling the stop function when
// the server shuts down.
func StartMenutrackingPipeline(ctx context.Context, cfg PipelineConfig) (*PipelineResult, error) {
	// Propagate the configured River schema to the menutracking store package
	// so the discarded-jobs admin query reads from the right schema.
	menutrackingstore.SetRiverSchema(riverSchemaName())

	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres for pipeline: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}

	// Load sources and seed rate limiters.
	sources, err := menutracking.ListSources(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("loading sources: %w", err)
	}
	domains := make([]string, len(sources))
	for i, s := range sources {
		domains[i] = s.Domain
	}
	limiterMap := menutracking.NewDomainLimiterMap(1, 1) // 1 request/sec, burst 1
	limiterMap.Seed(domains)

	// Build river workers.
	workers := river.NewWorkers()
	promotionWorker := &menutracking.RulePromotionWorker{
		Pool:    pool,
		Fetcher: cfg.Fetcher,
	}
	scrapeWorker := &menutracking.ScrapeWorker{
		Pool:         pool,
		Fetcher:      cfg.Fetcher,
		RateLimiters: limiterMap,
		AgentConfig:  menutracking.DefaultAgentPathConfig(),
		VectorSink:   cfg.VectorSink,
		ChatBackend:  cfg.ChatBackend,
	}
	river.AddWorker(workers, scrapeWorker)
	river.AddWorker(workers, promotionWorker)

	discoverWorker := &menusearch.DiscoverMenuURLWorker{
		Store:                menusearch.NewStore(pool),
		GenAIClient:          cfg.GenAIClient,
		AvroDestDir:          cfg.DiscoveryAvroDestDir,
		GeminiModel:          cfg.DiscoveryGeminiModel,
		ScrapeStaggerSeconds: cfg.DiscoveryStaggerSeconds,
		MaxNoURLAttempts:     cfg.DiscoveryMaxNoURLAttempts,
		MaxAttempts:          cfg.ScrapeMaxAttempts,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	if cfg.GenAIClient != nil {
		river.AddWorker(workers, discoverWorker)
		if cfg.MenuStore == nil || cfg.Embedder == nil || cfg.Extractor == nil {
			slog.Warn("menusearch: discover worker registered but scrape worker is not — discovered URLs will be queued but not processed",
				"has_menu_store", cfg.MenuStore != nil,
				"has_embedder", cfg.Embedder != nil,
				"has_extractor", cfg.Extractor != nil)
		}
	}

	scrapeMenuWorker := &menusearch.ScrapeMenuWorker{
		Store:           menusearch.NewStore(pool),
		MenuStore:       cfg.MenuStore,
		Embedder:        cfg.Embedder,
		Fetcher:         cfg.Fetcher,
		Extractor:       cfg.Extractor,
		AvroDestDir:     cfg.ExtractionAvroDestDir,
		EnableVision:    cfg.EnableVision,
		UsePdftotext:    cfg.UsePdftotext,
		WebagentAdapter: cfg.WebagentAdapter,
		BronzeDir:       cfg.BronzeDir,
	}
	if cfg.MenuStore != nil && cfg.Embedder != nil && cfg.Extractor != nil {
		river.AddWorker(workers, scrapeMenuWorker)
	}

	// Build periodic jobs from sources.
	var periodicJobs []*river.PeriodicJob
	for _, s := range sources {
		schedule, err := parseCronSchedule(s.CronSchedule)
		if err != nil {
			slog.Warn("skipping source with invalid cron schedule", "source", s.ID, "cron", s.CronSchedule, "err", err)
			continue
		}
		pj := river.NewPeriodicJob(schedule, func() (river.JobArgs, *river.InsertOpts) {
			return menutracking.ScrapeJobArgs{
					SourceID: s.ID,
					URL:      s.URL,
					Domain:   s.Domain,
				}, &river.InsertOpts{
					MaxAttempts: cfg.ScrapeMaxAttempts,
					UniqueOpts: river.UniqueOpts{
						ByPeriod: 7 * 24 * time.Hour,
					},
				}
		}, &river.PeriodicJobOpts{RunOnStart: false})
		periodicJobs = append(periodicJobs, pj)
	}

	riverClient, err := newRiverClient(pool, &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
		Workers:                     workers,
		PeriodicJobs:                periodicJobs,
		DiscardedJobRetentionPeriod: 30 * 24 * time.Hour,
		ErrorHandler:                &deadLetterHandler{pool: pool, restaurantStore: menusearch.NewStore(pool)},
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("creating river client: %w", err)
	}
	scrapeWorker.RiverClient = riverClient
	discoverWorker.RiverClient = riverClient

	jobQueue := &menusearch.JobQueue{Client: riverClient, MaxAttempts: cfg.ScrapeMaxAttempts}

	if err := riverClient.Start(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("starting river client: %w", err)
	}

	slog.Info("menutracking pipeline started", "sources", len(sources))

	// Return a stop function that drains the river client and closes the pool.
	stop := func(stopCtx context.Context) error {
		slog.Info("shutting down menutracking pipeline")
		if err := riverClient.Stop(stopCtx); err != nil {
			return fmt.Errorf("stopping river client: %w", err)
		}
		pool.Close()
		return nil
	}

	return &PipelineResult{
		Stop:               stop,
		Pool:               pool,
		RestaurantStore:    menusearch.NewStore(pool),
		RestaurantJobQueue: jobQueue,
	}, nil
}

// ErrInvalidCron is returned when a cron schedule expression is not supported.
var ErrInvalidCron = errors.New("invalid --cron")

// parseCronSchedule converts common cron expressions to river.PeriodicSchedule.
// Phase 1 supports simple interval-based schedules. Full robfig/cron parsing
// will be added in a later phase.
func parseCronSchedule(cronExpr string) (river.PeriodicSchedule, error) {
	switch cronExpr {
	case "@daily", "@midnight", "0 0 * * *":
		return river.PeriodicInterval(24 * time.Hour), nil
	case "@hourly", "0 * * * *":
		return river.PeriodicInterval(1 * time.Hour), nil
	case "@weekly", "0 0 * * 0":
		return river.PeriodicInterval(7 * 24 * time.Hour), nil
	default:
		return nil, fmt.Errorf("%w: unsupported schedule %q: only @daily, @hourly, @weekly supported in phase 1", ErrInvalidCron, cronExpr)
	}
}
