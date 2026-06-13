package menutracking

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"fodmap/chat"
	"fodmap/menutracking/store"
	"fodmap/scraper"
	"fodmap/search"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// VectorSink inserts regulatory updates into a vector store (Weaviate,
// Postgres+pgvector, etc.). The ScrapeWorker calls it after persisting to
// Postgres. This interface decouples the worker from the concrete search
// backend.
type VectorSink interface {
	BatchUpsertRegulatory(ctx context.Context, items []search.RegulatoryUpdate) error
}

// RiverInserter is the interface for inserting new river jobs. Implemented by
// *river.Client[pgx.Tx], this interface allows tests to provide a stub.
type RiverInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// ScrapeJobArgs are the arguments for a ScrapeWorker job. Each job scrapes a
// single URL from a single source.
type ScrapeJobArgs struct {
	SourceID string `json:"source_id" jsonschema:"required"`
	URL      string `json:"url" jsonschema:"required"`
	Domain   string `json:"domain" jsonschema:"required"`
}

func (ScrapeJobArgs) Kind() string { return "menutracking.scrape" }

// RulePromotionJobArgs are the arguments for a RulePromotionWorker job.
type RulePromotionJobArgs struct {
	RuleID   string `json:"rule_id" jsonschema:"required"`
	SourceID string `json:"source_id" jsonschema:"required"`
	URL      string `json:"url" jsonschema:"required"`
}

func (RulePromotionJobArgs) Kind() string { return "menutracking.rule_promotion" }

// DefaultScrapeMaxAttempts overrides river's default of 25 retries for
// scrape jobs. Transient HTTP errors should retry quickly; permanent ones
// should surface fast.
const DefaultScrapeMaxAttempts = 8

// BronzeDir is the root directory for raw scraped content storage. Override
// for tests.
var BronzeDir = "data/bronze"

// ScrapeWorker processes a single scrape job: fetches the page, applies the
// fast path if an active rule exists for the domain, falls back to the agent
// path otherwise, persists the result to Postgres and the vector sink, and
// enqueues a RulePromotionWorker if a rule was proposed.
type ScrapeWorker struct {
	river.WorkerDefaults[ScrapeJobArgs]

	Pool         *pgxpool.Pool
	Fetcher      scraper.Fetcher
	RateLimiters *DomainLimiterMap
	AgentConfig  AgentPathConfig
	RiverClient  RiverInserter
	VectorSink   VectorSink
	ChatBackend  chat.ChatBackend
}

func (w *ScrapeWorker) Work(ctx context.Context, job *river.Job[ScrapeJobArgs]) error {
	args := job.Args

	// Step 4: Per-domain rate limiting before any HTTP call.
	if err := w.RateLimiters.Wait(ctx, args.Domain); err != nil {
		return fmt.Errorf("rate limiter cancelled: %w", err)
	}

	slog.Info("menutracking scrape", "source_id", args.SourceID, "url", args.URL, "attempt", job.Attempt)

	// Fetch the page first — both fast path and agent path need it.
	fetchResult, err := w.Fetcher.Fetch(ctx, args.URL)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", args.URL, err)
	}

	rawBytes, err := scraper.RawHTMLBody(fetchResult.Body)
	_ = fetchResult.Body.Close()
	if err != nil {
		return fmt.Errorf("reading body from %s: %w", args.URL, err)
	}

	pageContent := scraper.TrafilaturaFallback(string(rawBytes))

	// Persist raw content to bronze layer.
	scrapeDate := time.Now().UTC().Format("2006-01-02")
	bronzePath := filepath.Join(BronzeDir, args.Domain, scrapeDate, fmt.Sprintf("%s.html", args.SourceID))
	if err := writeBronzeFile(bronzePath, rawBytes); err != nil {
		slog.Warn("menutracking: failed to write bronze file", "path", bronzePath, "err", err)
	}

	var update *StructuredUpdate
	var proposedRuleID string

	// Step 5: Fast path — try active extraction rule on fetched content.
	fastResult, err := ApplyRule(ctx, w.Pool, args.Domain, pageContent)
	if err != nil {
		slog.Warn("menutracking fast path error", "domain", args.Domain, "err", err)
	}

	if fastResult != nil && fastResult.Extracted != nil {
		slog.Info("menutracking fast path hit", "domain", args.Domain, "substance", fastResult.Extracted.SubstanceName)
		update = fastResult.Extracted
	} else {
		// Step 6: Agent path — LLM extraction.
		agentResult, err := ExtractWithAgent(ctx, w.ChatBackend, args.URL, args.Domain, pageContent, w.AgentConfig)
		if err != nil {
			return fmt.Errorf("agent path for %s: %w", args.URL, err)
		}
		if agentResult.Update != nil {
			update = agentResult.Update
		}

		// Step 7: If the agent proposed a rule, persist it and enqueue promotion.
		if agentResult.RuleText != "" {
			rule := &ExtractionRule{
				Domain:     args.Domain,
				Selector:   agentResult.RuleText,
				Fields:     map[string]string{},
				Status:     RuleStatusProposed,
				Provenance: args.URL,
			}
			if err := InsertProposedRule(ctx, w.Pool, rule); err != nil {
				slog.Warn("menutracking: failed to insert proposed rule", "domain", args.Domain, "err", err)
			} else {
				proposedRuleID = rule.ID
			}
		}
	}

	if update == nil {
		return fmt.Errorf("no update produced for %s (fast path=%v)", args.URL, fastResult != nil && fastResult.Extracted != nil)
	}

	// Fill source URL from the scrape job if not set by extraction.
	if update.SourceURL == "" {
		update.SourceURL = args.URL
	}

	// Step 8: Persist to Postgres.
	if err := upsertUpdate(ctx, w.Pool, args.SourceID, bronzePath, update); err != nil {
		return fmt.Errorf("persisting update for %s: %w", args.URL, err)
	}

	// Step 8b: Upsert to vector sink (Weaviate).
	if w.VectorSink != nil {
		vecItem := search.RegulatoryUpdate{
			ID:            uuid.NewSHA1(uuid.NameSpaceURL, []byte(args.SourceID+update.CASNumber+update.SubstanceName)).String(),
			SourceID:      args.SourceID,
			SourceURL:     update.SourceURL,
			CASNumber:     update.CASNumber,
			SubstanceName: update.SubstanceName,
			ChangeType:    string(update.ChangeType),
			Description:   update.Description,
			EffectiveDate: update.EffectiveDate,
		}
		if err := w.VectorSink.BatchUpsertRegulatory(ctx, []search.RegulatoryUpdate{vecItem}); err != nil {
			slog.Warn("menutracking: vector sink upsert failed", "err", err)
		}
	}

	// Enqueue rule promotion if a rule was proposed.
	if proposedRuleID != "" {
		_, err := w.RiverClient.Insert(ctx, RulePromotionJobArgs{
			RuleID:   proposedRuleID,
			SourceID: args.SourceID,
			URL:      args.URL,
		}, &river.InsertOpts{MaxAttempts: 3})
		if err != nil {
			slog.Warn("menutracking: failed to enqueue rule promotion", "rule_id", proposedRuleID, "err", err)
		}
	}

	return nil
}

// RulePromotionWorker verifies a proposed extraction rule by re-running it
// against the same content the agent used. If the output matches the agent's
// extract, the rule is promoted to active; otherwise it is rejected.
type RulePromotionWorker struct {
	river.WorkerDefaults[RulePromotionJobArgs]

	Pool    *pgxpool.Pool
	Fetcher scraper.Fetcher
}

func (w *RulePromotionWorker) Work(ctx context.Context, job *river.Job[RulePromotionJobArgs]) error {
	args := job.Args

	slog.Info("menutracking rule promotion", "rule_id", args.RuleID, "source_id", args.SourceID)

	// Verify the proposed rule by re-running it against fetched content.
	rule, err := GetProposedRule(ctx, w.Pool, args.RuleID)
	if err != nil {
		return fmt.Errorf("fetching proposed rule %s for verification: %w", args.RuleID, err)
	}
	if rule == nil {
		slog.Warn("menutracking: proposed rule no longer found, skipping promotion", "rule_id", args.RuleID)
		return nil
	}

	// Re-fetch the page to verify the rule against live content.
	fetchResult, err := w.Fetcher.Fetch(ctx, args.URL)
	if err != nil {
		return fmt.Errorf("refetching %s for rule verification: %w", args.URL, err)
	}
	rawBytes, err := scraper.RawHTMLBody(fetchResult.Body)
	_ = fetchResult.Body.Close()
	if err != nil {
		return fmt.Errorf("reading body for verification %s: %w", args.URL, err)
	}
	pageContent := scraper.TrafilaturaFallback(string(rawBytes))

	// Apply the proposed rule and check if it produces valid output.
	result, err := ApplyRuleWithSelector(ctx, w.Pool, rule, pageContent)
	if err != nil {
		slog.Warn("menutracking: proposed rule failed verification, rejecting", "rule_id", args.RuleID, "err", err)
		if rejectErr := RejectRule(ctx, w.Pool, args.RuleID); rejectErr != nil {
			slog.Warn("menutracking: failed to reject rule", "rule_id", args.RuleID, "err", rejectErr)
		}
		return nil
	}
	if result == nil || result.Extracted == nil {
		slog.Warn("menutracking: proposed rule produced no output, rejecting", "rule_id", args.RuleID)
		if rejectErr := RejectRule(ctx, w.Pool, args.RuleID); rejectErr != nil {
			slog.Warn("menutracking: failed to reject rule", "rule_id", args.RuleID, "err", rejectErr)
		}
		return nil
	}

	// Rule verification passed — promote to active.
	if err := PromoteRule(ctx, w.Pool, args.RuleID); err != nil {
		return fmt.Errorf("promoting rule %s: %w", args.RuleID, err)
	}

	return nil
}

// writeBronzenFile writes raw content to the bronze layer at the given path.
// It creates intermediate directories as needed.
func writeBronzeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating bronze directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing bronze file: %w", err)
	}
	return nil
}

// upsertUpdate inserts or updates a regulatory_update row using a
// deterministic ID derived from source_id + date + substance identifier.
func upsertUpdate(ctx context.Context, pool *pgxpool.Pool, sourceID, bronzePath string, u *StructuredUpdate) error {
	dateKey := time.Now().UTC().Format("2006-01-02")
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(sourceID+dateKey+u.CASNumber+u.SubstanceName)).String()
	now := time.Now()
	_, err := pool.Exec(ctx, store.UpsertRegulatoryUpdateSQL,
		id, sourceID, u.SourceURL, u.CASNumber, u.SubstanceName, string(u.ChangeType), u.Description, u.EffectiveDate, bronzePath, now)
	if err != nil {
		return fmt.Errorf("upserting regulatory update: %w", err)
	}
	return nil
}

// DeadLetterHandler persists discarded river jobs to the menutracking_dead_letter
// table for long-term audit. Install as river's ErrorHandler or call from a
// periodic cleanup job.
func DeadLetterHandler(ctx context.Context, pool *pgxpool.Pool, jobKind string, jobArgs json.RawMessage, errMsg string) error {
	_, err := pool.Exec(ctx,
		"INSERT INTO menutracking_dead_letter (job_kind, job_args, error) VALUES ($1, $2, $3)",
		jobKind, jobArgs, errMsg)
	if err != nil {
		return fmt.Errorf("writing to dead letter: %w", err)
	}
	return nil
}
