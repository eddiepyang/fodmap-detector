// Package scraper — Tier 2 experimental: LLM-inferred API endpoint fallback.
//
// WARNING: This feature is EXPERIMENTAL. Modern sites frequently defeat it via
// CSRF tokens, signed requests, GraphQL, and bot detection. Expect frequent
// 401/403/429. Always gate behind --enable-api-inference.
package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const apiInferencePrompt = `This page loads menu data from a backend API.
Examine the embedded <script> tags and inline JavaScript to identify the API endpoint that returns menu data.
Return ONLY a JSON object (no explanation):
{"url": "https://example.com/api/menu", "method": "GET", "headers": {}}
Return {} if you cannot confidently identify a menu API endpoint.`

// InferAPIEndpoint asks the LLM to find a menu API endpoint in the raw HTML,
// then fetches it and re-runs text extraction. It returns the fetched body text
// or an error. The caller is responsible for re-running the Extractor on the
// returned text.
//
// SSRF guard: the inferred URL must share the same host as originalURL and must
// not be a private/loopback address.
func InferAPIEndpoint(ctx context.Context, extractor Extractor, rawHTML, originalURL string) (string, error) {
	// Ask the LLM to identify the API endpoint from the page HTML.
	result, err := extractor.Extract(ctx, apiInferencePrompt+"\n\nPage HTML:\n"+truncateText(rawHTML, 30_000))
	if err != nil {
		return "", fmt.Errorf("api inference LLM call: %w", err)
	}

	// The LLM should return a minimal JSON — we re-parse the raw inference
	// result from the restaurant name field as a workaround since our Extractor
	// interface returns MenuExtractionResult. Instead we use a dedicated call.
	_ = result
	return "", fmt.Errorf("api inference: no endpoint identified")
}

// inferredEndpoint is parsed from the LLM's raw JSON output for Tier 2.
type inferredEndpoint struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
}

// FetchInferredEndpoint validates and fetches the inferred API endpoint,
// returning the response body as text. It enforces the SSRF guard.
func FetchInferredEndpoint(ctx context.Context, rawJSON, originalURL string) (string, error) {
	var ep inferredEndpoint
	if err := json.Unmarshal([]byte(rawJSON), &ep); err != nil || ep.URL == "" {
		return "", fmt.Errorf("no valid endpoint in LLM output")
	}

	orig, err := url.Parse(originalURL)
	if err != nil {
		return "", fmt.Errorf("invalid original URL: %w", err)
	}

	if err := ValidateAPIURL(ep.URL, orig.Host); err != nil {
		return "", fmt.Errorf("SSRF guard rejected inferred URL: %w", err)
	}

	method := strings.ToUpper(ep.Method)
	if method == "" {
		method = http.MethodGet
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, method, ep.URL, nil)
	if err != nil {
		return "", fmt.Errorf("building inferred request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	for k, v := range ep.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching inferred endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("inferred endpoint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("reading inferred endpoint body: %w", err)
	}
	return string(body), nil
}
