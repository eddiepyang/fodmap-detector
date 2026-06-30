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
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

// Modifier is a size, add-on, choice, or variant of a menu item with its own
// price. Only literally-present data is recorded — never inferred.
type Modifier struct {
	Name  string   `json:"name"`
	Price *float64 `json:"price,omitempty"`
}

// MenuEntry holds a single extracted menu item. Only ingredients literally
// stated on the menu are recorded — never inferred.
type MenuEntry struct {
	DishName           string     `json:"dish"`
	Description        string     `json:"description"`
	Price              *float64   `json:"price,omitempty"`
	Section            string     `json:"section,omitempty"`
	StatedIngredients  []string   `json:"stated_ingredients"`
	HasFullIngredients bool       `json:"has_full_ingredients"`
	Modifiers          []Modifier `json:"modifiers,omitempty"`
}

// MenuExtractionResult is the structured output of the scrape pipeline.
type MenuExtractionResult struct {
	RestaurantName string      `json:"restaurant_name"`
	City           string      `json:"city,omitempty"`
	State          string      `json:"state,omitempty"`
	SourceURL      string      `json:"source_url"`
	Address        string      `json:"address"`
	PhoneNumber    string      `json:"phone_number"`
	ScrapedAtUTC   string      `json:"scraped_at_utc"`
	Items          []MenuEntry `json:"items"`
	// ExtractionTier records which cascade tier produced this result (e.g.
	// "jsonld", "html_llm", "pdf", "image_ocr", "webagent"). It is pipeline
	// metadata, not part of the service menu document — set by ExtractMenu and
	// persisted per scrape for tier-mix telemetry. See pipeline.Tier* constants.
	ExtractionTier string `json:"extraction_tier,omitempty"`
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

// RenderedFetcher retrieves a URL that may require JavaScript rendering and
// returns its body and content-type. Implementations will use a headless
// browser (e.g. chromedp) behind the existing --enable-js-render flag. This
// interface is defined now so menutracking can reference it; actual
// implementations will be added in a later phase.
type RenderedFetcher interface {
	Fetcher
	FetchRendered(ctx context.Context, rawURL string) (FetchResult, error)
}

// Extractor converts page text (or images via the vision path) into a
// MenuExtractionResult. Multiple implementations are provided (OpenAI-compat,
// Gemini). Tests stub this interface directly.
type Extractor interface {
	Extract(ctx context.Context, pageText string) (MenuExtractionResult, error)
}

// HTTPStatusError is returned by HTTPFetcher.Fetch when the server responds with
// a non-200 HTTP status. It carries the status code so callers can distinguish
// retryable blocks (403/429) from hard failures (404/5xx) without string-matching.
type HTTPStatusError struct {
	StatusCode int
	URL        string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("unexpected status %d for %s", e.StatusCode, e.URL)
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
		return FetchResult{}, &HTTPStatusError{StatusCode: resp.StatusCode, URL: rawURL}
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

// jsShellMinRawBytes is the minimum raw HTML size at which a tiny visible-text
// body is plausibly the product of a JS bundle (scripts, asset hosts, inlined
// state) rather than a genuinely small static page. Below this, the ratio is
// meaningless (a 50-byte "closed" page is not a shell, just small).
const jsShellMinRawBytes = 50_000

// jsShellMinRatio is the raw-bytes-to-visible-runes ratio above which the page
// is a JS shell: the static HTML is dominated by script bundles / inlined
// state while the actual menu content is injected client-side. Real content
// pages cluster at 1–200×; JS shells at >1000×. The 500× threshold sits in a
// wide empty gap with no observed overlap in either direction.
const jsShellMinRatio = 500

// IsJSShell reports whether the page is a JS-framework shell whose menu
// content is injected client-side and therefore missing from the static HTML.
//
// Heuristic: the raw HTML is large (≥ jsShellMinRawBytes) yet yields little
// visible text, so the raw-bytes-to-visible-runes ratio exceeds
// jsShellMinRatio. This is framework-agnostic — it does not depend on a
// maintained list of framework markers, so it does not rot when Wix renames
// an asset host or a new SPA framework appears. The 500-rune visible floor
// keeps it orthogonal to the 200-rune tooShort gate: a 200–500 rune Wix
// homepage (which passes tooShort) is still flagged, while a real content
// page (> 500 runes of prose) is left on the normal LLM text path.
func IsJSShell(md, rawHTML string) bool {
	visible := utf8.RuneCountInString(strings.TrimSpace(md))
	if visible >= 500 {
		return false
	}
	if len(rawHTML) < jsShellMinRawBytes {
		return false
	}
	return len(rawHTML)/max(visible, 1) > jsShellMinRatio
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

// ── Menu image detection (Phase C) ──────────────────────────────────────────

// menuImageCandidate describes an <img> that might be a menu image.
type menuImageCandidate struct {
	src    string
	width  int
	height int
	score  int
}

// minMenuImageScore is the minimum score an <img> must reach to be considered a
// menu image. Size alone (≥800px) gives +3, which is below the threshold — so a
// large hero/banner photo with no menu signal is rejected. A real menu image
// picks up at least one more signal (filename keyword +3, #menu context +2, or
// alt "menu" +2), clearing the threshold. The post-extraction "0 items ⇒ not
// a menu" validation (cli/scrape.go) is the real safety net; this heuristic
// gate only reduces wasted OCR calls on obvious non-menus.
const minMenuImageScore = 4

// FindMenuImage scans HTML for the largest <img> likely to be an embedded menu
// image (e.g. a photo of a printed trifold menu). Returns the absolute URL of
// the best candidate and true if one is found, or "" and false otherwise.
//
// Heuristics (any subset can match; higher score wins):
//   - Size: width/height ≥ 800px, or a srcset descriptor ≥1024w.
//   - Filename: src matches /menu|trifold|menu-card|food|drink/i.
//   - Context: the <img> is inside an element with id containing "menu".
//   - Alt: alt is empty (text menus use descriptive alt) or contains "menu".
//
// Excludes: images inside <header>/<footer>/<nav>, tiny icons (width < 100),
// and images whose filename/alt screams non-menu (logo, hero, banner, press,
// award) — the penalty drops them below minMenuImageScore even if alt contains
// "menu". pageURL is used to resolve relative src values into absolute URLs.
func FindMenuImage(htmlBytes []byte, contentType, pageURL string) (string, bool) {
	cands, ok := FindMenuImages(htmlBytes, contentType, pageURL)
	if !ok {
		return "", false
	}
	return cands[0], true
}

// FindMenuImages is the top-N variant of FindMenuImage. It returns the menu-
// image candidates above minMenuImageScore in descending score order (ties
// broken by largest dimensions), as absolute URLs. The caller may try them in
// order until one yields extracted items (bounded N, e.g. 2), since pages mix a
// hero photo with a real menu image. Returns (urls, true) iff at least one
// candidate clears the threshold.
func FindMenuImages(htmlBytes []byte, contentType, pageURL string) ([]string, bool) {
	cr, err := charset.NewReader(bytes.NewReader(htmlBytes), contentType)
	if err != nil {
		return nil, false
	}
	doc, err := html.Parse(cr)
	if err != nil {
		return nil, false
	}

	page, err := url.Parse(pageURL)
	if err != nil {
		return nil, false
	}

	var candidates []menuImageCandidate

	var walk func(*html.Node, bool, bool)
	walk = func(n *html.Node, inSkip bool, inMenuCtx bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "header", "footer", "nav":
				inSkip = true
			}
			// Track whether we're inside an element with id containing "menu".
			for _, a := range n.Attr {
				if a.Key == "id" && strings.Contains(strings.ToLower(a.Val), "menu") {
					inMenuCtx = true
				}
			}
			if n.Data == "img" {
				if !inSkip {
					if c := evaluateImg(n, inMenuCtx); c.score >= minMenuImageScore {
						candidates = append(candidates, c)
					}
				}
				return // don't recurse into <img>
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inSkip, inMenuCtx)
		}
	}
	walk(doc, false, false)

	if len(candidates) == 0 {
		return nil, false
	}

	// Sort by score desc, then by largest dimensions desc (deterministic order
	// for equal scores so the top-N selection is stable across runs).
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].width*candidates[i].height > candidates[j].width*candidates[j].height
	})

	urls := make([]string, 0, len(candidates))
	for _, c := range candidates {
		abs := resolveURL(c.src, page)
		if abs == "" {
			continue
		}
		urls = append(urls, abs)
	}
	if len(urls) == 0 {
		return nil, false
	}
	return urls, true
}

// evaluateImg scores an <img> node against the menu-image heuristics.
func evaluateImg(n *html.Node, inMenuCtx bool) menuImageCandidate {
	c := menuImageCandidate{}

	var dataSrc string
	for _, a := range n.Attr {
		switch a.Key {
		case "src":
			c.src = a.Val
		case "data-src":
			dataSrc = a.Val
		case "srcset":
			if w := maxSrcsetWidth(a.Val); w > c.width {
				c.width = w
			}
		case "width":
			if w, err := strconv.Atoi(a.Val); err == nil && w > c.width {
				c.width = w
			}
		case "height":
			if h, err := strconv.Atoi(a.Val); err == nil && h > c.height {
				c.height = h
			}
		case "alt":
			if a.Val == "" {
				c.score += 1 // empty alt is a mild positive signal for menu images
			} else if strings.Contains(strings.ToLower(a.Val), "menu") {
				c.score += 2
			}
		}
	}

	// Prefer src; fall back to data-src (lazy-loaded images) if src is absent.
	if c.src == "" {
		c.src = dataSrc
	}

	if c.src == "" {
		return menuImageCandidate{} // no usable src
	}

	// Exclude tiny icons.
	if c.width > 0 && c.width < 100 {
		return menuImageCandidate{}
	}

	// Size heuristic: large image (≥800px in any dimension, or srcset ≥1024w).
	if c.width >= 800 || c.height >= 800 {
		c.score += 3
	}

	// Filename heuristic: positive keywords.
	lower := strings.ToLower(c.src)
	for _, kw := range []string{"menu", "trifold", "menu-card", "food-menu", "drink-menu", "food", "drink"} {
		if strings.Contains(lower, kw) {
			c.score += 3
			break
		}
	}

	// Filename heuristic: negative keywords. A logo/hero/banner/press/award
	// image is almost never a menu even if its alt says "menu" (e.g. a press
	// badge labeled "menu of awards"). The penalty is large enough to cancel
	// the size bonus plus one alt bonus, dropping the image below threshold.
	for _, kw := range []string{"logo", "hero", "banner", "press", "award", "badge"} {
		if strings.Contains(lower, kw) {
			c.score -= 6
			break
		}
	}

	// .svg files are vector logos/icons, not menu photos — reject outright.
	if strings.HasSuffix(lower, ".svg") {
		return menuImageCandidate{}
	}

	// Context heuristic: inside an element with id containing "menu".
	if inMenuCtx {
		c.score += 2
	}

	return c
}

// maxSrcsetWidth extracts the largest width descriptor from a srcset string
// (e.g. "menu.png 1024w, menu-2x.png 2048w" → 2048).
func maxSrcsetWidth(srcset string) int {
	maxW := 0
	for _, part := range strings.Split(srcset, ",") {
		part = strings.TrimSpace(part)
		// Descriptor is the last token, e.g. "1024w" or "2x".
		fields := strings.Fields(part)
		if len(fields) < 2 {
			continue
		}
		desc := fields[len(fields)-1]
		if strings.HasSuffix(desc, "w") {
			if w, err := strconv.Atoi(strings.TrimSuffix(desc, "w")); err == nil && w > maxW {
				maxW = w
			}
		}
	}
	return maxW
}

// resolveURL resolves a possibly-relative URL against a base URL.
func resolveURL(raw string, base *url.URL) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return base.ResolveReference(u).String()
}
