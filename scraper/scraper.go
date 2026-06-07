// Package scraper fetches restaurant menu pages and extracts structured menu
// data using a configurable LLM backend.
package scraper

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"github.com/markusmobius/go-trafilatura"
)

const (
	// MaxBodyBytes is the maximum response body size we will read.
	MaxBodyBytes = 20 * 1024 * 1024 // 20 MB

	// MaxInputChars is the character limit sent to the LLM.
	MaxInputChars = 60_000

	// userAgent is sent with every HTTP request.
	userAgent = "fodmap-detector/0.1 (+https://github.com/edwardpyang/fodmap-detector)"

	// minTextChars is the minimum chars from PDF text-layer to trust it.
	minPDFTextChars = 200
)

// MenuEntry holds a single extracted menu item. Only ingredients literally
// stated on the menu are recorded — never inferred.
type MenuEntry struct {
	DishName           string   `json:"dish"`
	Description        string   `json:"description"`
	StatedIngredients  []string `json:"stated_ingredients"`
	HasFullIngredients bool     `json:"has_full_ingredients"`
}

// MenuExtractionResult is the structured output of the scrape pipeline.
type MenuExtractionResult struct {
	RestaurantName string      `json:"restaurant_name"`
	City           string      `json:"city,omitempty"`
	State          string      `json:"state,omitempty"`
	SourceURL      string      `json:"source_url"`
	ScrapedAtUTC   string      `json:"scraped_at_utc"`
	Items          []MenuEntry `json:"items"`
}

// FetchResult is the return value of Fetcher.Fetch.
type FetchResult struct {
	Body        io.ReadCloser
	ContentType string
}

// Fetcher retrieves a URL and returns its body and content-type.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) (FetchResult, error)
}

// Extractor converts page text (or images via the vision path) into a
// MenuExtractionResult. Multiple implementations are provided (OpenAI-compat,
// Gemini). Tests stub this interface directly.
type Extractor interface {
	Extract(ctx context.Context, pageText string) (MenuExtractionResult, error)
}

// HTTPFetcher is the production Fetcher. It enforces a body-size cap, sends a
// polite User-Agent, decodes non-UTF-8 HTML, and optionally checks robots.txt.
type HTTPFetcher struct {
	Client       *http.Client
	IgnoreRobots bool
}

// NewHTTPFetcher returns a Fetcher with a 30 s HTTP timeout.
// The LLM timeout must be handled separately — local models can take minutes.
func NewHTTPFetcher(ignoreRobots bool) *HTTPFetcher {
	return &HTTPFetcher{
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
		IgnoreRobots: ignoreRobots,
	}
}

// Fetch retrieves rawURL and returns the body capped at MaxBodyBytes.
func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL string) (FetchResult, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return FetchResult{}, fmt.Errorf("invalid URL: %w", err)
	}

	if !f.IgnoreRobots {
		if err := checkRobots(ctx, f.Client, u, userAgent); err != nil {
			return FetchResult{}, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := f.Client.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("fetching URL: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return FetchResult{}, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, rawURL)
	}

	ct := resp.Header.Get("Content-Type")
	body := http.MaxBytesReader(nil, resp.Body, MaxBodyBytes)
	return FetchResult{Body: body, ContentType: ct}, nil
}

// checkRobots fetches /robots.txt and returns an error if the path is
// disallowed for our UA. Errors fetching robots.txt are silently ignored
// (we treat them as "allowed").
func checkRobots(ctx context.Context, client *http.Client, u *url.URL, ua string) error {
	robotsURL := &url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Path:   "/robots.txt",
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL.String(), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil
	}

	path := u.Path
	if path == "" {
		path = "/"
	}
	return parseRobotsTxt(string(body), ua, path)
}

// parseRobotsTxt is a minimal robots.txt parser: handles User-agent and
// Disallow directives. Returns an error if the path is disallowed.
func parseRobotsTxt(body, ua, path string) error {
	var applicable bool
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "user-agent:") {
			agent := strings.TrimSpace(line[len("user-agent:"):])
			applicable = agent == "*" || strings.Contains(strings.ToLower(ua), strings.ToLower(agent))
		}
		if applicable && strings.HasPrefix(lower, "disallow:") {
			disallowed := strings.TrimSpace(line[len("disallow:"):])
			if disallowed != "" && strings.HasPrefix(path, disallowed) {
				return fmt.Errorf("robots.txt disallows %s for our user-agent (use --ignore-robots to override)", path)
			}
		}
	}
	return nil
}

// ConvertHTMLToMarkdown walks an HTML parse tree and emits Markdown-flavored
// text. It skips script, style, nav, footer, and other non-content elements.
// The output preserves heading hierarchy and list structure, which helps the
// LLM identify menu sections.
func ConvertHTMLToMarkdown(r io.Reader, contentType string) (string, error) {
	// Wrap in charset-aware reader so non-UTF-8 pages decode correctly.
	cr, err := charset.NewReader(r, contentType)
	if err != nil {
		return "", fmt.Errorf("charset reader: %w", err)
	}

	doc, err := html.Parse(cr)
	if err != nil {
		return "", fmt.Errorf("html parse: %w", err)
	}

	var b strings.Builder
	walkHTML(&b, doc, 0)
	return b.String(), nil
}

var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true,
	"head": true, "nav": true, "footer": true, "iframe": true,
	"button": true, "svg": true, "img": true,
}

func walkHTML(b *strings.Builder, n *html.Node, depth int) {
	if n.Type == html.ElementNode {
		if skipTags[n.Data] {
			return
		}
		switch n.Data {
		case "h1":
			b.WriteString("\n# ")
		case "h2":
			b.WriteString("\n## ")
		case "h3":
			b.WriteString("\n### ")
		case "h4", "h5", "h6":
			b.WriteString("\n#### ")
		case "li":
			b.WriteString("\n- ")
		case "p", "div", "section", "article", "tr":
			b.WriteString("\n")
		}
	}

	if n.Type == html.TextNode {
		s := strings.TrimSpace(n.Data)
		if s != "" {
			b.WriteString(s)
			b.WriteString(" ")
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkHTML(b, c, depth+1)
	}

	// Add newline after block-level closing tags.
	if n.Type == html.ElementNode {
		switch n.Data {
		case "h1", "h2", "h3", "h4", "h5", "h6", "li", "p", "tr":
			b.WriteString("\n")
		}
	}
}

// trafilaturaFallback uses go-trafilatura to extract the main content block
// from HTML when the raw Markdown conversion is too noisy. It is applied as a
// Tier 1.5 fallback when the primary conversion yields poor results.
func trafilaturaFallback(rawHTML string) string {
	r := strings.NewReader(rawHTML)
	result, err := trafilatura.Extract(r, trafilatura.Options{
		IncludeLinks:    false,
		ExcludeTables:   false,
		IncludeImages:   false,
		ExcludeComments: true,
	})
	if err != nil || result == nil {
		return ""
	}
	return result.ContentText
}

// isTooNoisy returns true when the Markdown output is dominated by navigation
// links and boilerplate rather than content. Heuristic: more than 70% of
// non-empty lines are very short (≤ 20 chars), typical of nav link labels.
func IsTooNoisy(md string) bool {
	lines := strings.Split(md, "\n")
	var total, short int
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		total++
		if utf8.RuneCountInString(l) <= 20 {
			short++
		}
	}
	if total < 20 {
		return false
	}
	return float64(short)/float64(total) > 0.70
}

// truncateText trims s to at most maxChars characters, logging a warning when
// truncation occurs.
func truncateText(s string, maxChars int) string {
	if len([]rune(s)) <= maxChars {
		return s
	}
	runes := []rune(s)
	kept := string(runes[:maxChars])
	dropped := len(runes) - maxChars
	slog.Warn("scraper input truncated", "kept", maxChars, "dropped", dropped)
	return kept
}

// isPrivateHost returns true for loopback and RFC-1918 private addresses.
// Used in SSRF validation for Tier 2 API inference.
func isPrivateHost(host string) bool {
	// Strip port if present.
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	private := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"::1/128",
		"169.254.0.0/16", // link-local / AWS metadata
		"fc00::/7",
	}
	for _, cidr := range private {
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

// ValidateAPIURL checks that u is safe to follow as a Tier 2 inferred endpoint.
// It must share the same host as originalHost, and must not resolve to a
// private/loopback address (SSRF guard).
func ValidateAPIURL(rawURL, originalHost string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host != originalHost {
		return fmt.Errorf("inferred API URL host %q does not match original host %q", u.Host, originalHost)
	}
	if isPrivateHost(u.Host) {
		return fmt.Errorf("inferred API URL resolves to a private/loopback address: %s", u.Host)
	}
	return nil
}

// businessNamespace is the UUID namespace for deterministic business IDs.
var businessNamespace = uuid.MustParse("f0d6c8a0-e2b4-4d8a-9f1c-3b7a5d9e2c40")

// BusinessID returns a stable, collision-resistant identifier for a restaurant
// derived from the URL's host. This prevents /menu/lunch and /menu/dinner from
// being treated as separate restaurants.
func BusinessID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	host := strings.ToLower(u.Hostname())
	return uuid.NewSHA1(businessNamespace, []byte(host)).String()
}

// MenuSection derives a short section name from a URL path for use in the
// menuSection field when JSON-LD doesn't provide one.
func MenuSection(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	return strings.ReplaceAll(last, "-", " ")
}

// RawHTMLBody reads all bytes from r so that we can pass the string to both
// the HTML converter and trafilatura.
func RawHTMLBody(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// TrafilaturaFallback uses go-trafilatura to extract the main content block
// from HTML. Exported so cli/scrape.go can call it for the boilerplate-heavy
// fallback path.
func TrafilaturaFallback(rawHTML string) string {
	return trafilaturaFallback(rawHTML)
}
