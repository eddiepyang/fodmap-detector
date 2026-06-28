package menusearch

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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

	// Primary: authoritative URLs from GroundingChunks (what Gemini actually cited).
	var rawURLs []string
	for _, cand := range res.Candidates {
		if cand.GroundingMetadata == nil {
			continue
		}
		for _, chunk := range cand.GroundingMetadata.GroundingChunks {
			if chunk.Web != nil && chunk.Web.URI != "" {
				rawURLs = append(rawURLs, chunk.Web.URI)
			}
		}
	}
	// GroundingChunks return vertexaisearch.cloud.google.com redirect URLs —
	// resolve them to the real restaurant domain before filtering.
	rawURLs = resolveRedirects(ctx, w.HTTPClient, rawURLs)

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
	if err := json.Unmarshal([]byte(cleanText), &result); err != nil {
		slog.Warn("failed to parse discovery JSON, falling back to regex", "err", err)
		urlRe := regexp.MustCompile(`https?://[^\s)+"']+`)
		for _, u := range urlRe.FindAllString(text, -1) {
			if !strings.Contains(u, "vertexaisearch") && !strings.Contains(u, "google.com") {
				rawURLs = append(rawURLs, u)
			}
		}
	} else {
		if result.WebsiteURL != "" {
			rawURLs = append(rawURLs, result.WebsiteURL)
		}
		rawURLs = append(rawURLs, result.MenuURLs...)
	}

	foundURLs := dedupAndFilter(rawURLs)
	primaryURL := result.WebsiteURL
	if primaryURL == "" && len(foundURLs) > 0 {
		primaryURL = foundURLs[0] // fallback
	}

	if len(foundURLs) == 0 && len(rawURLs) > 0 {
		logger.Info("all grounding chunks were filtered out (likely delivery/directory sites)", "raw_count", len(rawURLs))
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
		logger.Info("found URL(s)", "count", len(foundURLs), "primary", primaryURL)

		if err := w.Store.UpdateDiscoveryURLs(ctx, args.CAMIS, primaryURL, foundURLs, "gemini", result.Address, result.PhoneNumber); err != nil {
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

// resolveRedirects follows HTTP redirects (up to 5 hops) and returns the final
// destination URLs. Converts Gemini's vertexaisearch redirect URLs into real domains.
// Ported from scripts/menuscan/main.go.
func resolveRedirects(ctx context.Context, client *http.Client, urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		final := u
		cur := u
		for i := 0; i < 5; i++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodHead, cur, nil)
			if err != nil {
				break
			}
			req.Header.Set("User-Agent", "fodmap-menusearch/1.0")
			resp, err := client.Do(req)
			if err != nil {
				break
			}
			loc := resp.Header.Get("Location")
			_ = resp.Body.Close()
			if loc == "" {
				break
			}
			locURL, err := url.Parse(loc)
			if err != nil {
				break
			}
			if locURL.IsAbs() {
				final = loc
			} else {
				base, _ := url.Parse(cur)
				if base != nil {
					final = base.ResolveReference(locURL).String()
				}
			}
			cur = final
		}
		out = append(out, final)
	}
	return out
}

// dedupAndFilter deduplicates URLs and drops delivery/social/review platforms,
// returning own-domain URLs only. Ported from scripts/menuscan/main.go.
func dedupAndFilter(urls []string) []string {
	seen := make(map[string]struct{}, len(urls))
	var out []string
	for _, u := range urls {
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		l := strings.ToLower(u)
		if strings.Contains(l, "facebook.com") ||
			strings.Contains(l, "instagram.com") ||
			strings.Contains(l, "yelp.com") ||
			strings.Contains(l, "tripadvisor.com") ||
			strings.Contains(l, "google.com/maps") ||
			strings.Contains(l, "ubereats.com") ||
			strings.Contains(l, "doordash.com") ||
			strings.Contains(l, "grubhub.com") ||
			strings.Contains(l, "postmates.com") ||
			strings.Contains(l, "seamless.com") ||
			strings.Contains(l, "delivery.com") {
			continue
		}
		out = append(out, u)
	}
	return out
}
