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

type inferredEndpoint struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
}

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