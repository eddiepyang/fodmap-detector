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
	res, err := e.embedRaw(ctx, "search_query: "+text)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	return res[0], nil
}

// EmbedBatch returns embeddings for multiple texts.
func (e *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	// Prepend "search_document: " for indexing
	processedTexts := make([]string, len(texts))
	for i, t := range texts {
		processedTexts[i] = "search_document: " + t
	}

	res, err := e.embedRaw(ctx, processedTexts)
	if err != nil {
		return nil, err
	}
	if len(res) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(res))
	}
	return res, nil
}

func (e *OllamaEmbedder) embedRaw(ctx context.Context, input any) ([][]float32, error) {
	reqBody := ollamaRequest{
		Model: e.model,
		Input: input,
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("ollama returned 404: ensure model '%s' is pulled (run 'ollama pull %s')", e.model, e.model)
		}
		return nil, fmt.Errorf("unexpected status from ollama: %s", resp.Status)
	}

	var res ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return res.Embeddings, nil
}

// Close releases any resources held by the embedder.
func (e *OllamaEmbedder) Close() error {
	return nil
}
