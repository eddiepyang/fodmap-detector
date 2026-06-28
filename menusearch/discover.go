package menusearch

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
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
		rawURLs = append(rawURLs, urlRe.FindAllString(text, -1)...)
	} else {
		if len(result.MenuURLs) > 0 {
			rawURLs = append(rawURLs, result.MenuURLs...)
		} else if result.WebsiteURL != "" {
			rawURLs = append(rawURLs, result.WebsiteURL)
		}
	}

	// Remove hard-blocked non-menu hosts (grounding redirects, real-estate, hotels, directories).
	var dedupedURLs []string
	for _, u := range dedup(rawURLs) {
		if isNonMenuURL(u) {
			logger.Info("dropping non-menu URL (blocklist)", "url", u)
			continue
		}
		dedupedURLs = append(dedupedURLs, u)
	}

	// Probe reachability: drop only genuinely dead domains (DNS failure, ECONNREFUSED, TLS errors).
	// Keep any URL that returns an HTTP response (even 403/429/5xx) or times out.
	httpClient := w.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 8 * time.Second}
	}
	candidates := reachableMenuURLs(ctx, httpClient, dedupedURLs)
	for _, u := range dedupedURLs {
		found := false
		for _, c := range candidates {
			if c == u {
				found = true
				break
			}
		}
		if !found {
			logger.Info("dropping dead-domain URL (unreachable)", "url", u)
		}
	}

	// Separate direct restaurant URLs from delivery platform URLs.
	// Prefer direct URLs; fall back to delivery-platform URLs if nothing else found.
	var directURLs, deliveryURLs []string
	for _, u := range candidates {
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
	if (primaryURL == "" || isDeliveryURL(primaryURL)) && len(foundURLs) > 0 {
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

// nonMenuHosts is a package-level blocklist of host substrings that are NEVER
// menus. Matched case-insensitively. Extend this slice as new junk hosts appear.
var nonMenuHosts = []string{
	// Gemini grounding-redirect artifacts — CRITICAL, must drop first.
	"vertexaisearch.cloud.google.com",
	// Real-estate listings.
	"loopnet.com",
	"streeteasy.com",
	"realtor.com",
	"zillow.com",
	"crexi.com",
	// Hotel booking.
	"hilton.com",
	"marriott.com",
	"booking.com",
	"expedia.com",
	"hotels.com",
	// Business directories.
	"checkle.com",
	"mapquest.com",
	"yellowpages.com",
	"bbb.org",
}

// isNonMenuURL returns true when the URL's host matches one of the hard-blocked
// non-menu domains. These URLs must never be used even as fallback.
func isNonMenuURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, blocked := range nonMenuHosts {
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return true
		}
	}
	return false
}

// isDeadDomainErr returns true when err represents a genuinely dead/unreachable
// domain: DNS resolution failure, connection refused, or TLS certificate error.
// HTTP-level errors (4xx, 5xx) and timeouts are NOT dead-domain signals — those
// are anti-bot blocks that the webagent bypass handles downstream.
func isDeadDomainErr(err error) bool {
	if err == nil {
		return false
	}
	// DNS: no such host.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return true
	}
	// Connection refused (port closed, server not listening).
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	// TLS certificate errors (invalid cert, expired, etc.).
	var certErr x509.CertificateInvalidError
	if errors.As(err, &certErr) {
		return true
	}
	var unknownAuthErr x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthErr) {
		return true
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return true
	}
	// tls.AlertError covers handshake failures surfaced by the TLS package.
	var tlsAlert tls.AlertError
	return errors.As(err, &tlsAlert)
}

// reachableMenuURLs probes each URL and returns those that are NOT dead domains.
// A URL is kept if it returns any HTTP response (including 403/429/5xx) or if
// the probe times out — those indicate live anti-bot protected servers. Only
// genuine dead-domain errors (DNS NXDOMAIN, ECONNREFUSED, TLS errors) cause a
// URL to be dropped. Probes run concurrently with a concurrency limit of 5.
func reachableMenuURLs(ctx context.Context, client *http.Client, urls []string) []string {
	const concurrency = 5

	type result struct {
		url  string
		keep bool
	}

	results := make([]result, len(urls))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, u := range urls {
		wg.Add(1)
		go func(idx int, rawURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			keep := probeURL(ctx, client, rawURL)
			results[idx] = result{url: rawURL, keep: keep}
		}(i, u)
	}
	wg.Wait()

	out := make([]string, 0, len(urls))
	for _, r := range results {
		if r.keep {
			out = append(out, r.url)
		}
	}
	return out
}

// probeURL sends a HEAD (falling back to GET on non-dead-domain errors) and
// returns true if the host appears live. Private/loopback hosts are skipped
// (treated as keep=false to avoid SSRF probing).
func probeURL(ctx context.Context, client *http.Client, rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	// SSRF guard: skip private/loopback hosts.
	if isPrivateMenuHost(parsed.Hostname()) {
		return false
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		return true // any HTTP response means the host is live
	}
	if isDeadDomainErr(err) {
		return false
	}
	// Non-dead-domain HEAD error (e.g. redirect loop, keep-alive issues):
	// try GET as fallback before giving up.
	req2, err2 := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err2 != nil {
		return true // can't construct GET; assume live (conservative)
	}
	resp2, err2 := client.Do(req2)
	if err2 == nil {
		_ = resp2.Body.Close()
		return true
	}
	// If the GET also failed, only drop on confirmed dead-domain signal.
	return !isDeadDomainErr(err2)
}

// isPrivateMenuHost returns true for loopback and RFC-1918 addresses (SSRF guard).
func isPrivateMenuHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	privateNets := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"::1/128",
		"169.254.0.0/16",
		"fc00::/7",
	}
	for _, cidr := range privateNets {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
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
