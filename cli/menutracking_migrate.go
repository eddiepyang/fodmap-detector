package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os/signal"
	"syscall"
	"time"

	"fodmap/chat"
	"fodmap/menutracking"
	"fodmap/scraper"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
	menutrackingAddSourceCmd.Flags().String("cron", "@weekly", "Cron schedule (e.g. '@weekly', '@daily', '0 6 * * 1-5')")
	menutrackingAddSourceCmd.Flags().Int("max-tokens", 32000, "Max input tokens per source page")
}

func runMenutrackingAddSource(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
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
	DSN          string
	Fetcher      scraper.Fetcher
	VectorSink   menutracking.VectorSink
	ChatBackend  chat.ChatBackend
	ReloadSignal <-chan struct{} // optional: written to on SIGHUP or POST /menutracking/reload
}

// PipelineResult holds the running pipeline's stop function and references
// needed for runtime operations like source reloading.
type PipelineResult struct {
	Stop func(context.Context) error
	Pool *pgxpool.Pool
}

// deadLetterHandler implements river.ErrorHandler to persist discarded jobs
// to the menutracking_dead_letter table for long-term audit.
type deadLetterHandler struct {
	pool *pgxpool.Pool
}

func (h *deadLetterHandler) HandleError(ctx context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	jobKind := job.Kind
	var jobArgs json.RawMessage
	if job.EncodedArgs != nil {
		jobArgs = job.EncodedArgs
	}
	if dlErr := menutracking.DeadLetterHandler(ctx, h.pool, jobKind, jobArgs, err.Error()); dlErr != nil {
		slog.Warn("menutracking: failed to write dead letter", "err", dlErr)
	}
	return nil
}

func (h *deadLetterHandler) HandlePanic(ctx context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	jobKind := job.Kind
	var jobArgs json.RawMessage
	if job.EncodedArgs != nil {
		jobArgs = job.EncodedArgs
	}
	errMsg := fmt.Sprintf("panic: %v\n%s", panicVal, trace)
	if dlErr := menutracking.DeadLetterHandler(ctx, h.pool, jobKind, jobArgs, errMsg); dlErr != nil {
		slog.Warn("menutracking: failed to write dead letter for panic", "err", dlErr)
	}
	return nil
}

// StartMenutrackingPipeline creates the pgxpool, river client, and workers, and
// starts the pipeline. It returns a stop function that drains in-flight jobs
// on shutdown. The caller is responsible for calling the stop function when
// the server shuts down.
func StartMenutrackingPipeline(ctx context.Context, cfg PipelineConfig) (*PipelineResult, error) {
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

	// Build periodic jobs from sources.
	var periodicJobs []*river.PeriodicJob
	for _, s := range sources {
		s := s
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
					MaxAttempts: menutracking.DefaultScrapeMaxAttempts,
					UniqueOpts: river.UniqueOpts{
						ByPeriod: 7 * 24 * time.Hour,
					},
				}
		}, &river.PeriodicJobOpts{RunOnStart: false})
		periodicJobs = append(periodicJobs, pj)
	}

	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
		Workers:                     workers,
		PeriodicJobs:                periodicJobs,
		DiscardedJobRetentionPeriod: 30 * 24 * time.Hour,
		ErrorHandler:                &deadLetterHandler{pool: pool},
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("creating river client: %w", err)
	}
	scrapeWorker.RiverClient = riverClient

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
		Stop: stop,
		Pool: pool,
	}, nil
}

// Deprecated: use StartMenutrackingPipeline instead. This function exists for
// backward compatibility with the CLI menutracking subcommand.
func RunMenutrackingPipeline(dsn string, fetcher scraper.Fetcher) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	stopFn, err := StartMenutrackingPipeline(ctx, PipelineConfig{
		DSN:     dsn,
		Fetcher: fetcher,
	})
	if err != nil {
		return err
	}

	// Wait for signal.
	<-ctx.Done()
	slog.Info("shutting down menutracking pipeline")

	drainCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return stopFn.Stop(drainCtx)
}

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
		return nil, fmt.Errorf("unsupported cron schedule %q: only @daily, @hourly, @weekly supported in phase 1", cronExpr)
	}
}
