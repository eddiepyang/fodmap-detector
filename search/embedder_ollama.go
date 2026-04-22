package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// OllamaEmbedder implements Embedder using a local or remote Ollama server.
type OllamaEmbedder struct {
	url    string
	model  string
	client *http.Client
}

// NewOllamaEmbedder creates a new Ollama embedder.
// url should be the base URL of the Ollama server, e.g. "http://localhost:11434"
func NewOllamaEmbedder(url, model string) *OllamaEmbedder {
	return &OllamaEmbedder{
		url:    url,
		model:  model,
		client: &http.Client{},
	}
}

type ollamaRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

type ollamaResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// EmbedSingle returns the embedding for a single text.
func (e *OllamaEmbedder) EmbedSingle(ctx context.Context, text string) ([]float32, error) {
	// For nomic-embed-text we prepend "search_query: " at query time.
	reqBody := ollamaRequest{
		Model: e.model,
		Input: "search_query: " + text,
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.url+"/api/embed", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	var res ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(res.Embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return res.Embeddings[0], nil
}

// EmbedBatch returns embeddings for multiple texts.
func (e *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := ollamaRequest{
		Model: e.model,
		Input: texts,
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.url+"/api/embed", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	var res ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(res.Embeddings) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(res.Embeddings))
	}

	return res.Embeddings, nil
}

// Close releases any resources held by the embedder.
func (e *OllamaEmbedder) Close() error {
	return nil
}
