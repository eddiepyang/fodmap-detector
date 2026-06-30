package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Verify TEIEmbedder implements Embedder at compile time.
var _ Embedder = (*TEIEmbedder)(nil)

// TEIEmbedder implements Embedder against a HuggingFace Text Embeddings
// Inference (TEI) service. It serves nomic-embed-text (768-dim) by default and
// keeps the same "search_query: "/"search_document: " prefix convention as
// OllamaEmbedder so the two are interchangeable for nomic-embed-text.
//
// TEI's /embed endpoint accepts {"inputs": [...], "truncate": true} and returns
// either {"embeddings": [[...]]} (older builds) or the bare array [[...]]
// (newer builds / the /embed endpoint vs. the Rust-native /embed). We handle
// both shapes via content-type sniffing + a try-both decode.
type TEIEmbedder struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewTEIEmbedder creates a TEI embedder pointed at baseURL (e.g.
// "http://localhost:8080"). model is informational — TEI is configured at
// server startup with a single model and ignores the per-request model
// field, but we keep it for symmetry with OllamaEmbedder and for telemetry.
func NewTEIEmbedder(baseURL, model string) *TEIEmbedder {
	return &TEIEmbedder{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// teiRequest is the body sent to TEI's /embed endpoint.
type teiRequest struct {
	Inputs   []string `json:"inputs"`
	Truncate bool     `json:"truncate"`
}

// teiResponse is the older JSON-object response shape.
type teiResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// EmbedSingle prepends the query-time prefix and returns one vector.
func (e *TEIEmbedder) EmbedSingle(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.embed(ctx, []string{"search_query: " + text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("tei returned no embeddings")
	}
	return vecs[0], nil
}

// EmbedBatch prepends the document-time prefix per text and returns vectors
// in input order. An empty input slice returns nil, nil.
func (e *TEIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	in := make([]string, len(texts))
	for i, t := range texts {
		in[i] = "search_document: " + t
	}
	vecs, err := e.embed(ctx, in)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(texts) {
		return nil, fmt.Errorf("tei returned %d embeddings, expected %d", len(vecs), len(texts))
	}
	return vecs, nil
}

// Close is a no-op for the HTTP client.
func (e *TEIEmbedder) Close() error { return nil }

// embed posts the inputs to /embed and decodes the response, tolerating both
// the {"embeddings": [...]} and bare [[...]] shapes.
func (e *TEIEmbedder) embed(ctx context.Context, inputs []string) ([][]float32, error) {
	reqBody, err := json.Marshal(teiRequest{Inputs: inputs, Truncate: true})
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embed", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.model != "" {
		req.Header.Set("X-Model", e.model)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tei returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Try the object shape first ({"embeddings": [...]}). Fall back to the
	// bare array shape ([[...]]) used by newer TEI builds.
	var obj teiResponse
	if err := json.Unmarshal(body, &obj); err == nil && obj.Embeddings != nil {
		return obj.Embeddings, nil
	}
	var bare [][]float32
	if err := json.Unmarshal(body, &bare); err != nil {
		return nil, fmt.Errorf("decode tei response (tried object and bare array): %w", err)
	}
	return bare, nil
}
