package scraper

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	_ "embed"
)

//go:embed scrape-prompt.txt
var menuPrompt string

// OpenAICompatExtractor calls any OpenAI-compatible /v1/chat/completions
// endpoint (Ollama, vLLM, OpenAI, LM Studio, vllm-metal, etc.).
type OpenAICompatExtractor struct {
	BaseURL string
	Model   string
	APIKey  string
	client  *http.Client
}

// NewOpenAICompatExtractor returns an extractor pointing at baseURL.
// The HTTP client timeout is intentionally generous (5 min) because local
// vision models processing large images can be slow.
func NewOpenAICompatExtractor(baseURL, model, apiKey string) *OpenAICompatExtractor {
	return &OpenAICompatExtractor{
		BaseURL: baseURL,
		Model:   model,
		APIKey:  apiKey,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

type chatMessage struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	ResponseFormat *respFormat   `json:"response_format,omitempty"`
	MaxTokens      int           `json:"max_tokens,omitempty"`
}

type respFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Extract sends pageText to the LLM and parses the returned JSON.
func (e *OpenAICompatExtractor) Extract(ctx context.Context, pageText string) (MenuExtractionResult, error) {
	pageText = truncateText(pageText, MaxInputChars)
	return e.chatJSON(ctx, menuPrompt+pageText, nil)
}

// ExtractImage sends image bytes (PNG) to a vision-capable LLM and parses the
// returned JSON. Used by the PDF vision path.
func (e *OpenAICompatExtractor) ExtractImage(ctx context.Context, pngBytes []byte) (MenuExtractionResult, error) {
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	dataURL := "data:image/png;base64," + b64
	return e.chatJSON(ctx, menuPrompt, &dataURL)
}

// chatJSON sends a chat completion request. If imageDataURL is non-nil the
// message uses image_url content parts (vision path); otherwise it's text-only.
func (e *OpenAICompatExtractor) chatJSON(ctx context.Context, prompt string, imageDataURL *string) (MenuExtractionResult, error) {
	var content []contentPart
	if imageDataURL != nil {
		content = []contentPart{
			{Type: "text", Text: prompt},
			{Type: "image_url", ImageURL: &imageURL{URL: *imageDataURL}},
		}
	} else {
		content = []contentPart{{Type: "text", Text: prompt}}
	}

	req := chatRequest{
		Model: e.Model,
		Messages: []chatMessage{
			{Role: "user", Content: content},
		},
		ResponseFormat: &respFormat{Type: "json_object"},
		MaxTokens:      4096,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return MenuExtractionResult{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return MenuExtractionResult{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+e.APIKey)
	}

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return MenuExtractionResult{}, fmt.Errorf("LLM request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return MenuExtractionResult{}, fmt.Errorf("LLM returned status %d: %s", resp.StatusCode, string(b))
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return MenuExtractionResult{}, fmt.Errorf("decode LLM response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return MenuExtractionResult{}, fmt.Errorf("LLM returned no choices")
	}

	raw := chatResp.Choices[0].Message.Content
	var result MenuExtractionResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return MenuExtractionResult{}, fmt.Errorf("parse LLM JSON output: %w (raw: %.200s)", err, raw)
	}
	return result, nil
}
