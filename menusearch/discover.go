package menusearch

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"google.golang.org/genai"
)

//go:embed prompts/discover.txt
var discoverPromptStr string

var discoverPromptTmpl = template.Must(template.New("discover").Parse(discoverPromptStr))

type DiscoverMenuURLWorker struct {
	river.WorkerDefaults[DiscoverMenuURLArgs]
	Store                *Store
	GenAIClient          *genai.Client
	RiverClient          *river.Client[pgx.Tx]
	HTTPClient           *http.Client
	AvroDestDir          string
	GeminiModel          string
	ScrapeStaggerSeconds int
	MaxNoURLAttempts     int
}

func (w *DiscoverMenuURLWorker) Work(ctx context.Context, job *river.Job[DiscoverMenuURLArgs]) error {
	args := job.Args
	logger := slog.With("job", job.ID, "camis", args.CAMIS, "dba", args.DBA)
	logger.Info("starting discovery job")

	// If a previous attempt already stored URLs (Gemini succeeded but the
	// subsequent DB write or enqueue failed), skip the expensive Gemini call
	// and go straight to re-enqueueing scrape jobs.
	if job.Attempt > 1 {
		if existing, err := w.Store.Get(ctx, args.CAMIS); err == nil && existing != nil && len(existing.MenuURLs) > 0 {
			logger.Info("reusing URLs from previous attempt, skipping Gemini call", "count", len(existing.MenuURLs))
			return w.enqueueScrapeJobs(ctx, args, existing.MenuURLs, "")
		}
	}

	var promptBuf bytes.Buffer
	if err := discoverPromptTmpl.Execute(&promptBuf, args); err != nil {
		return fmt.Errorf("execute prompt template: %w", err)
	}
	prompt := promptBuf.String()

	tool := &genai.Tool{
		GoogleSearch: &genai.GoogleSearch{},
	}

	res, err := w.GenAIClient.Models.GenerateContent(ctx, w.GeminiModel, genai.Text(prompt), &genai.GenerateContentConfig{
		Tools: []*genai.Tool{tool},
	})
	if err != nil {
		logger.Error("gemini request failed", "error", err)
		return err
	}

	if len(res.Candidates) == 0 {
		return fmt.Errorf("gemini returned no candidates")
	}

	if res.Candidates[0].Content == nil {
		return fmt.Errorf("gemini returned candidate with nil content (safety filtered)")
	}

	// Collect the full response text.
	var textBuilder strings.Builder
	for _, p := range res.Candidates[0].Content.Parts {
		if p.Text != "" {
			textBuilder.WriteString(p.Text)
		}
	}
	text := textBuilder.String()

	var result struct {
		WebsiteURL  string   `json:"website_url"`
		MenuURLs    []string `json:"menu_urls"`
		Address     string   `json:"address"`
		PhoneNumber string   `json:"phone_number"`
	}

	cleanText := strings.TrimSpace(text)
	cleanText = strings.TrimPrefix(cleanText, "```json")
	cleanText = strings.TrimPrefix(cleanText, "```")
	cleanText = strings.TrimSuffix(cleanText, "```")
	cleanText = strings.TrimSpace(cleanText)

	var rawURLs []string
	if err := json.Unmarshal([]byte(cleanText), &result); err != nil {
		slog.Warn("failed to parse discovery JSON, falling back to regex", "err", err)
		urlRe := regexp.MustCompile(`https?://[^\s)+"']+`)
		for _, u := range urlRe.FindAllString(text, -1) {
			rawURLs = append(rawURLs, u)
		}
	} else {
		if result.WebsiteURL != "" {
			rawURLs = append(rawURLs, result.WebsiteURL)
		}
		rawURLs = append(rawURLs, result.MenuURLs...)
	}

	// Separate direct restaurant URLs from delivery platform URLs.
	// Prefer direct URLs; fall back to delivery-platform URLs if nothing else found.
	var directURLs, deliveryURLs []string
	for _, u := range dedup(rawURLs) {
		if isDeliveryURL(u) {
			deliveryURLs = append(deliveryURLs, u)
		} else {
			directURLs = append(directURLs, u)
		}
	}

	foundURLs := directURLs
	urlSource := "gemini"
	if len(foundURLs) == 0 && len(deliveryURLs) > 0 {
		foundURLs = deliveryURLs
		urlSource = "gemini_delivery"
		logger.Info("no direct URL found, using delivery platform URLs", "count", len(deliveryURLs))
	}

	primaryURL := result.WebsiteURL
	if primaryURL == "" && len(foundURLs) > 0 {
		primaryURL = foundURLs[0]
	}

	eventID := uuid.NewString()
	record := GeminiDiscoveryRecord{
		CAMIS:        args.CAMIS,
		DBA:          args.DBA,
		Prompt:       prompt,
		ResponseText: text,
		SourceURLs:   foundURLs,
		Model:        w.GeminiModel,
		EventID:      eventID,
		JobID:        fmt.Sprintf("%d", job.ID),
		Attempt:      job.Attempt,
	}

	avroDest := filepath.Join(w.AvroDestDir, fmt.Sprintf("%s-%d.avro", args.CAMIS, job.Attempt))
	if err := WriteGeminiDiscoveryAvro(ctx, avroDest, record); err != nil {
		slog.Warn("failed to write gemini discovery avro", "error", err, "path", avroDest)
	}

	if len(foundURLs) > 0 {
		logger.Info("found URL(s)", "count", len(foundURLs), "primary", primaryURL, "source", urlSource)

		if err := w.Store.UpdateDiscoveryURLs(ctx, args.CAMIS, primaryURL, foundURLs, urlSource, result.Address, result.PhoneNumber); err != nil {
			return fmt.Errorf("update menu url: %w", err)
		}

		if err := w.enqueueScrapeJobs(ctx, args, foundURLs, eventID); err != nil {
			return err
		}
	} else {
		maxAttempts := w.MaxNoURLAttempts
		if maxAttempts <= 0 {
			maxAttempts = 3
		}
		if job.Attempt >= maxAttempts {
			logger.Info("no URL found after max attempts, marking permanently", "camis", args.CAMIS, "max_attempts", maxAttempts)
			if err := w.Store.UpdateDiscoveryURLs(ctx, args.CAMIS, "", []string{}, "gemini", "", ""); err != nil {
				return fmt.Errorf("update no-url status: %w", err)
			}
			return nil
		}
		logger.Info("no URL found, will retry", "camis", args.CAMIS, "attempt", job.Attempt, "max", maxAttempts)
		return fmt.Errorf("no URL found for %s (attempt %d/%d)", args.CAMIS, job.Attempt, maxAttempts)
	}

	return nil
}

func (w *DiscoverMenuURLWorker) enqueueScrapeJobs(ctx context.Context, args DiscoverMenuURLArgs, menuURLs []string, eventID string) error {
	for i, menuURL := range menuURLs {
		stagger := w.ScrapeStaggerSeconds
		if stagger <= 0 {
			stagger = 15
		}
		delay := time.Duration(i*stagger) * time.Second
		_, err := w.RiverClient.Insert(ctx, ScrapeMenuArgs{
			CAMIS:            args.CAMIS,
			URL:              menuURL,
			DBA:              args.DBA,
			DiscoveryEventID: eventID,
		}, &river.InsertOpts{
			UniqueOpts: river.UniqueOpts{
				ByArgs:   true,
				ByPeriod: 30 * 24 * time.Hour,
			},
			ScheduledAt: time.Now().Add(delay),
		})
		if err != nil {
			return fmt.Errorf("enqueue scrape for %s: %w", menuURL, err)
		}
	}
	return nil
}

// dedup returns urls with duplicates removed, preserving order.
func dedup(urls []string) []string {
	seen := make(map[string]struct{}, len(urls))
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

// isDeliveryURL reports whether u belongs to a third-party delivery or
// review platform rather than the restaurant's own domain.
func isDeliveryURL(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, "doordash.com") ||
		strings.Contains(l, "seamless.com") ||
		strings.Contains(l, "grubhub.com") ||
		strings.Contains(l, "ubereats.com") ||
		strings.Contains(l, "postmates.com") ||
		strings.Contains(l, "delivery.com") ||
		strings.Contains(l, "yelp.com") ||
		strings.Contains(l, "tripadvisor.com") ||
		strings.Contains(l, "facebook.com") ||
		strings.Contains(l, "instagram.com") ||
		strings.Contains(l, "google.com/maps")
}
