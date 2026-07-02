package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PDFExtractor is implemented by extractors that can produce a structured menu
// directly from PDF bytes, bypassing the text→Extract path. The pure-Go
// OpenAICompatExtractor does not implement it; ServiceExtractor does.
type PDFExtractor interface {
	ExtractPDF(ctx context.Context, pdfBytes []byte) (MenuExtractionResult, error)
}

// JSRenderer is implemented by extractors that can scrape JS-rendered pages via
// the service's webagent endpoint. ServiceExtractor implements it when
// configured with an adapter ID (site/target). The pure-Go extractor does not.
type JSRenderer interface {
	// ScrapeJS calls the webagent scrape endpoint with the given params, then
	// structures the returned records via extractions:structure.
	// adapterID is "site/target"; params is the JSON body for the scrape call.
	ScrapeJS(ctx context.Context, adapterID string, params map[string]any) (MenuExtractionResult, error)
}

// ImageExtractor is implemented by extractors that can OCR a standalone image
// (e.g. a photo of a printed menu) and return a structured result. Used by the
// Phase C image-embedded-menu path. ServiceExtractor implements it.
type ImageExtractor interface {
	ExtractImage(ctx context.Context, imgBytes []byte, mime string) (MenuExtractionResult, error)
}

// ServiceExtractor routes all extraction — HTML/text, PDF, and image — to the
// Python scraper service's /v1 API. It fails if the service is unavailable.
type ServiceExtractor struct {
	baseURL    string
	pageClient *http.Client  // per-page request timeout (single inspect/extract/structure call)
	pdfClient  *http.Client  // per-request safety net for inspect/structure calls
	pdfTimeout time.Duration // wall-clock deadline across the whole PDF orchestration
}

// NewServiceExtractor builds a ServiceExtractor targeting the service at
// baseURL (e.g. "http://localhost:8765"). pageTimeout bounds each individual
// /pages:extract call; pdfTimeout bounds the whole PDF orchestration.
func NewServiceExtractor(baseURL string, pageTimeout, pdfTimeout time.Duration) *ServiceExtractor {
	return &ServiceExtractor{
		baseURL:    strings.TrimRight(baseURL, "/"),
		pageClient: &http.Client{Timeout: pageTimeout},
		pdfClient:  &http.Client{Timeout: pdfTimeout},
		pdfTimeout: pdfTimeout,
	}
}

// Extract routes HTML/text to the service's extractions:structure endpoint.
func (s *ServiceExtractor) Extract(ctx context.Context, pageText string) (MenuExtractionResult, error) {
	structRes, err := s.structure(ctx, pageText)
	if err != nil {
		return MenuExtractionResult{}, err
	}
	return mapStructureToResult(structRes), nil
}

// ── service request/response types ─────────────────────────────────────────

type serviceErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type serviceErrorEnvelope struct {
	Error serviceErrorDetail `json:"error"`
}

type pageRoute struct {
	Page          int      `json:"page"`
	Route         string   `json:"route"`
	TextChars     int      `json:"text_chars"`
	ImageCoverage *float64 `json:"image_coverage"`
}

type documentInspectResult struct {
	PageCount   int         `json:"page_count"`
	ContentType string      `json:"content_type"`
	Pages       []pageRoute `json:"pages"`
}

type extractPageResult struct {
	Page      int     `json:"page"`
	Route     string  `json:"route"`
	Backend   string  `json:"backend"`
	Text      *string `json:"text"`
	OcrText   *string `json:"ocr_text"`
	OcrLayout *string `json:"ocr_layout"`
}

type structureRequest struct {
	MergedText     string `json:"merged_text"`
	SchemaRevision string `json:"schema_revision"`
}

type menuSection struct {
	Name  string     `json:"name"`
	Items []menuItem `json:"items"`
}

type serviceModifier struct {
	Name  string   `json:"name"`
	Price *float64 `json:"price,omitempty"`
}

type menuItem struct {
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	Price              *float64          `json:"price,omitempty"`
	StatedIngredients  []string          `json:"stated_ingredients"`
	HasFullIngredients bool              `json:"has_full_ingredients"`
	Modifiers          []serviceModifier `json:"modifiers,omitempty"`
}

type menuDocument struct {
	SchemaRevision string        `json:"schema_revision"`
	RestaurantName string        `json:"restaurant_name"`
	City           string        `json:"city"`
	State          string        `json:"state"`
	Sections       []menuSection `json:"sections"`
}

type structureResult struct {
	SchemaRevision string       `json:"schema_revision"`
	Backend        string       `json:"backend"`
	Menu           menuDocument `json:"menu"`
}

// serviceError wraps a non-2xx service response, carrying the request id for
// cross-service debugging.
type serviceError struct {
	statusCode int
	code       string
	message    string
	requestID  string
}

func (e *serviceError) Error() string {
	if e.requestID != "" {
		return fmt.Sprintf("service %d %s: %s (request_id=%s)", e.statusCode, e.code, e.message, e.requestID)
	}
	return fmt.Sprintf("service %d %s: %s", e.statusCode, e.code, e.message)
}

// IsBackendUnavailable reports whether err is a 503 service error (OCR backend
// down) eligible for the pure-Go fallback.
func IsBackendUnavailable(err error) bool {
	var se *serviceError
	return errors.As(err, &se) && se.statusCode == http.StatusServiceUnavailable
}

// IsRenderTransient reports whether err is a transient error from the rendered-
// fetch endpoint (503 BrowserBusy/WafBlocked or 504 FetchTimeout). These should
// be retried by River rather than written as terminal failed_scrape status.
func IsRenderTransient(err error) bool {
	var se *serviceError
	if errors.As(err, &se) {
		return se.statusCode == http.StatusServiceUnavailable ||
			se.statusCode == http.StatusGatewayTimeout
	}
	return false
}

// ExtractPDF orchestrates the service's stateless flow:
// documents:inspect → per-page pages:extract → extractions:structure.
func (s *ServiceExtractor) ExtractPDF(ctx context.Context, pdfBytes []byte) (MenuExtractionResult, error) {
	// Enforce the overall PDF deadline across all per-page calls (the per-request
	// http.Client timeouts only bound a single call, not the N-page loop).
	ctx, cancel := context.WithTimeout(ctx, s.pdfTimeout)
	defer cancel()

	// 1. Inspect.
	inspect, err := s.inspectDocument(ctx, pdfBytes)
	if err != nil {
		return MenuExtractionResult{}, err
	}

	// 2. Extract each page, merge text (+layout).
	var merged strings.Builder
	for i, pg := range inspect.Pages {
		slog.Info("service extractor: extracting page",
			"page", pg.Page, "route", pg.Route, "total", len(inspect.Pages))
		start := time.Now()

		pageRes, err := s.extractPage(ctx, pdfBytes, pg.Page)
		if err != nil {
			return MenuExtractionResult{}, fmt.Errorf("extracting page %d: %w", pg.Page, err)
		}

		slog.Info("service extractor: page done",
			"page", pg.Page, "route", pageRes.Route, "backend", pageRes.Backend,
			"duration_ms", time.Since(start).Milliseconds())

		merged.WriteString(pageBlob(pageRes))
		if i < len(inspect.Pages)-1 {
			merged.WriteString("\n\n--- page break ---\n\n")
		}
	}

	// 3. Structure.
	structRes, err := s.structure(ctx, merged.String())
	if err != nil {
		return MenuExtractionResult{}, err
	}

	return mapStructureToResult(structRes), nil
}

// ExtractImage OCRs a standalone image (e.g. a photo of a printed menu) via the
// service's image-input path: inspect with Content-Type: image/* returns a
// single-page ocr decision, then pages:extract does the real OCR, then
// extractions:structure. This reuses the same orchestration as ExtractPDF but
// sends the image bytes with the original image MIME type (the service's
// image-input path skips PyMuPDF page logic per v1.py:52-58).
func (s *ServiceExtractor) ExtractImage(ctx context.Context, imgBytes []byte, mime string) (MenuExtractionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.pdfTimeout)
	defer cancel()

	if mime == "" {
		mime = "image/png"
	}

	// 1. Inspect — image input returns a single-page ocr decision.
	inspect, err := s.inspectDocumentWithMime(ctx, imgBytes, mime)
	if err != nil {
		return MenuExtractionResult{}, err
	}
	if len(inspect.Pages) == 0 {
		return MenuExtractionResult{}, fmt.Errorf("inspect returned no pages for image")
	}

	// 2. Extract the single page (OCR).
	slog.Info("service extractor: OCRing image", "route", inspect.Pages[0].Route, "mime", mime)
	start := time.Now()
	pageRes, err := s.extractPageWithMime(ctx, imgBytes, inspect.Pages[0].Page, mime)
	if err != nil {
		return MenuExtractionResult{}, fmt.Errorf("extracting image page %d: %w", inspect.Pages[0].Page, err)
	}
	slog.Info("service extractor: image OCR done",
		"route", pageRes.Route, "backend", pageRes.Backend,
		"duration_ms", time.Since(start).Milliseconds())

	// 3. Structure.
	structRes, err := s.structure(ctx, pageBlob(pageRes))
	if err != nil {
		return MenuExtractionResult{}, err
	}
	return mapStructureToResult(structRes), nil
}

// pageBlob builds the per-page text contribution sent to extractions:structure.
// For text-route pages we forward the text; for ocr-route pages we forward
// ocr_text concatenated with ocr_layout (layout signal — see plan Risks).
func pageBlob(p extractPageResult) string {
	var b strings.Builder
	if p.Text != nil && *p.Text != "" {
		b.WriteString(*p.Text)
	}
	if p.OcrText != nil && *p.OcrText != "" {
		b.WriteString(*p.OcrText)
		if p.OcrLayout != nil && *p.OcrLayout != "" {
			b.WriteString("\n\n[layout]\n")
			b.WriteString(*p.OcrLayout)
		}
	}
	return b.String()
}

// mapStructureToResult flattens the service MenuDocument into the detector's
// flat MenuExtractionResult. The section name from each MenuSection is
// carried into each item's Section field so multi-section menus preserve
// their structure (the storage layer tags each item with its section).
func mapStructureToResult(s structureResult) MenuExtractionResult {
	result := MenuExtractionResult{
		RestaurantName: s.Menu.RestaurantName,
		City:           s.Menu.City,
		State:          s.Menu.State,
	}
	for _, sec := range s.Menu.Sections {
		for _, item := range sec.Items {
			ingredients := item.StatedIngredients
			if ingredients == nil {
				ingredients = []string{}
			}
			mods := make([]Modifier, len(item.Modifiers))
			for i, m := range item.Modifiers {
				mods[i] = Modifier(m)
			}
			result.Items = append(result.Items, MenuEntry{
				DishName:           item.Name,
				Description:        item.Description,
				Price:              item.Price,
				Section:            sec.Name,
				StatedIngredients:  ingredients,
				HasFullIngredients: item.HasFullIngredients,
				Modifiers:          mods,
			})
		}
	}
	return result
}

// ── service HTTP helpers ────────────────────────────────────────────────────

func (s *ServiceExtractor) inspectDocument(ctx context.Context, body []byte) (documentInspectResult, error) {
	return s.inspectDocumentWithMime(ctx, body, "application/pdf")
}

func (s *ServiceExtractor) inspectDocumentWithMime(ctx context.Context, body []byte, contentType string) (documentInspectResult, error) {
	var res documentInspectResult
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/v1/documents:inspect", bytes.NewReader(body))
	if err != nil {
		return res, fmt.Errorf("build inspect request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := s.pdfClient.Do(req)
	if err != nil {
		return res, fmt.Errorf("inspect request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := decodeServiceError(resp); err != nil {
		return res, fmt.Errorf("inspect: %w", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return res, fmt.Errorf("decode inspect response: %w", err)
	}
	return res, nil
}

func (s *ServiceExtractor) extractPage(ctx context.Context, body []byte, page int) (extractPageResult, error) {
	return s.extractPageWithMime(ctx, body, page, "application/pdf")
}

func (s *ServiceExtractor) extractPageWithMime(ctx context.Context, body []byte, page int, contentType string) (extractPageResult, error) {
	var res extractPageResult
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/v1/documents/menu/pages/"+strconv.Itoa(page)+":extract",
		bytes.NewReader(body))
	if err != nil {
		return res, fmt.Errorf("build extract request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := s.pageClient.Do(req)
	if err != nil {
		return res, fmt.Errorf("extract page %d request: %w", page, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := decodeServiceError(resp); err != nil {
		return res, fmt.Errorf("extract page %d: %w", page, err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return res, fmt.Errorf("decode extract page %d response: %w", page, err)
	}
	return res, nil
}

func (s *ServiceExtractor) structure(ctx context.Context, mergedText string) (structureResult, error) {
	var res structureResult
	body, err := json.Marshal(structureRequest{
		MergedText:     mergedText,
		SchemaRevision: "v1",
	})
	if err != nil {
		return res, fmt.Errorf("marshal structure request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/v1/extractions:structure", bytes.NewReader(body))
	if err != nil {
		return res, fmt.Errorf("build structure request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.pdfClient.Do(req)
	if err != nil {
		return res, fmt.Errorf("structure request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := decodeServiceError(resp); err != nil {
		return res, fmt.Errorf("structure: %w", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return res, fmt.Errorf("decode structure response: %w", err)
	}
	itemCount := 0
	for _, sec := range res.Menu.Sections {
		itemCount += len(sec.Items)
	}
	slog.Info("service extractions:structure response",
		"backend", res.Backend,
		"schema_revision", res.SchemaRevision,
		"sections", len(res.Menu.Sections),
		"items", itemCount,
		"restaurant", res.Menu.RestaurantName,
		"input_chars", len(mergedText),
	)
	return res, nil
}

// decodeServiceError reads a non-2xx response and returns a *serviceError
// carrying the request id from the error envelope (or the X-Request-Id header
// for non-envelope bodies like validation 422s). Returns nil for 2xx.
func decodeServiceError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))

	// Try the standard error envelope first.
	var env serviceErrorEnvelope
	if json.Unmarshal(body, &env) == nil && env.Error.Code != "" {
		return &serviceError{
			statusCode: resp.StatusCode,
			code:       env.Error.Code,
			message:    env.Error.Message,
			requestID:  env.Error.RequestID,
		}
	}

	// Fallback: use X-Request-Id header if present.
	rid := resp.Header.Get("X-Request-Id")
	return &serviceError{
		statusCode: resp.StatusCode,
		code:       fmt.Sprintf("http_%d", resp.StatusCode),
		message:    strings.TrimSpace(string(body)),
		requestID:  rid,
	}
}

// An empty menu is returned by the service as a normal 200 with zero sections
// (see the service's MenuDocument contract), so it maps to a MenuExtractionResult
// with zero Items and needs no special error handling here — callers detect it
// via len(result.Items) == 0.

// ── webagent rendered-fetch (anti-scraping bypass) ─────────────────────────

// RenderOptions controls rendered-fetch behavior.
type RenderOptions struct {
	// NetworkIdle waits for networkidle after domcontentloaded before
	// serializing content. Needed for JS widgets (e.g. Wix
	// restaurant-menus-showcase-ooi) that fetch menu data via XHR after the
	// DOM is ready. Best-effort: if networkidle never fires, returns whatever
	// content is available.
	NetworkIdle bool
	// Scroll renders with a tall viewport and a progressive scroll pass so
	// lazy-loaded and virtualized menus (Grubhub loads each section when it
	// scrolls into view, and unmounts sections that scroll out) are fully
	// present in the serialized HTML.
	Scroll bool
}

// HTMLRenderer is implemented by extractors that can render an arbitrary URL
// in a headless browser and return the HTML. ServiceExtractor implements it.
// The capability is checked at runtime with a type assertion so the pipeline
// can fall back gracefully when the extractor is nil or the service is not
// configured.
type HTMLRenderer interface {
	FetchRenderedHTML(ctx context.Context, rawURL string, opts RenderOptions) (FetchResult, error)
}

// renderFetchRequest is the JSON body for POST /v1/webagent/fetch.
type renderFetchRequest struct {
	URL         string `json:"url"`
	NetworkIdle bool   `json:"network_idle"`
	Scroll      bool   `json:"scroll"`
}

// renderFetchResponse mirrors the Python response model for /v1/webagent/fetch.
type renderFetchResponse struct {
	HTML        string `json:"html"`
	ContentType string `json:"content_type"`
}

// FetchRenderedHTML renders rawURL in the Python webagent's headless browser and
// returns the resulting HTML as a FetchResult. It uses pdfClient (120 s) because
// a render can take up to ~75 s (FETCH_HARD_CAP_MS), which would exceed the 30 s
// pageClient timeout. Errors from the Python service (BrowserBusy 503,
// FetchTimeout 504, WafBlocked 503) surface as *serviceError so callers can
// apply retryable-error classification via IsBackendUnavailable.
func (s *ServiceExtractor) FetchRenderedHTML(ctx context.Context, rawURL string, opts RenderOptions) (FetchResult, error) {
	body, err := json.Marshal(renderFetchRequest{URL: rawURL, NetworkIdle: opts.NetworkIdle, Scroll: opts.Scroll})
	if err != nil {
		return FetchResult{}, fmt.Errorf("marshal render request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/v1/webagent/fetch", bytes.NewReader(body))
	if err != nil {
		return FetchResult{}, fmt.Errorf("build render request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	slog.Info("rendered-fetch: calling webagent", "url", rawURL)
	start := time.Now()

	resp, err := s.pdfClient.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("render request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := decodeServiceError(resp); err != nil {
		return FetchResult{}, fmt.Errorf("render: %w", err)
	}

	var rr renderFetchResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return FetchResult{}, fmt.Errorf("decode render response: %w", err)
	}

	slog.Info("rendered-fetch: done", "url", rawURL,
		"html_bytes", len(rr.HTML), "duration_ms", time.Since(start).Milliseconds())

	ct := rr.ContentType
	if ct == "" {
		ct = "text/html; charset=utf-8"
	}

	// Return the rendered HTML as a FetchResult so the normal pipeline cascade
	// (HTML→Markdown → JSON-LD → LLM) can process it, and the bronze layer
	// captures it (Finding F).
	return FetchResult{
		Body:        io.NopCloser(strings.NewReader(rr.HTML)),
		ContentType: ct,
	}, nil
}

// ── webagent (Phase B) ──────────────────────────────────────────────────────

// scrapeMeta mirrors the webagent's ScrapeMeta (subset we care about).
type scrapeMeta struct {
	Site   string `json:"site"`
	Target string `json:"target"`
	Empty  bool   `json:"empty"`
}

// scrapeResult mirrors the webagent's ScrapeResult.
type scrapeResult struct {
	Records []map[string]any `json:"records"`
	Meta    scrapeMeta       `json:"meta"`
}

// ScrapeJS calls the webagent scrape endpoint (Phase B), then structures the
// returned records via extractions:structure — the same converge-on-structuring
// pattern as the PDF flow. adapterID is "site/target"; params is the JSON body.
func (s *ServiceExtractor) ScrapeJS(ctx context.Context, adapterID string, params map[string]any) (MenuExtractionResult, error) {
	// 1. POST /v1/webagent/scrape/{site}/{target} with params as JSON body.
	// The webagent sub-app is mounted at /v1/webagent (see app.py), so the
	// full path is /v1/webagent/scrape/{site}/{target}.
	body, err := json.Marshal(params)
	if err != nil {
		return MenuExtractionResult{}, fmt.Errorf("marshal webagent params: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/v1/webagent/scrape/"+adapterID, bytes.NewReader(body))
	if err != nil {
		return MenuExtractionResult{}, fmt.Errorf("build webagent request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	slog.Info("webagent: scraping JS page", "adapter", adapterID)
	start := time.Now()

	resp, err := s.pdfClient.Do(req)
	if err != nil {
		return MenuExtractionResult{}, fmt.Errorf("webagent request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := decodeServiceError(resp); err != nil {
		return MenuExtractionResult{}, fmt.Errorf("webagent scrape: %w", err)
	}

	var sr scrapeResult
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return MenuExtractionResult{}, fmt.Errorf("decode webagent response: %w", err)
	}
	slog.Info("webagent: scrape done", "adapter", adapterID,
		"records", len(sr.Records), "empty", sr.Meta.Empty,
		"duration_ms", time.Since(start).Milliseconds())

	if len(sr.Records) == 0 {
		return MenuExtractionResult{}, nil
	}

	// 2. Serialize records into merged_text, then structure.
	merged := serializeRecords(sr.Records)
	structRes, err := s.structure(ctx, merged)
	if err != nil {
		return MenuExtractionResult{}, err
	}
	return mapStructureToResult(structRes), nil
}

// serializeRecords flattens the webagent's record dicts into a text blob
// suitable for extractions:structure. Each record's key→value pairs are joined
// into a line, and records are separated by blank lines.
func serializeRecords(records []map[string]any) string {
	var b strings.Builder
	for i, rec := range records {
		if i > 0 {
			b.WriteString("\n\n")
		}
		// Sort keys so the serialized blob (and thus the structuring LLM input)
		// is deterministic across runs — Go map iteration order is randomized.
		keys := make([]string, 0, len(rec))
		for k := range rec {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for j, k := range keys {
			if j > 0 {
				b.WriteString(" | ")
			}
			b.WriteString(k)
			b.WriteString(": ")
			fmt.Fprintf(&b, "%v", rec[k])
		}
	}
	return b.String()
}
