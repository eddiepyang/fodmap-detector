package scraper

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
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

// chatRequest represents the OpenAI-compatible /v1/chat/completions payload.
//
// NOTE ON max_tokens: We intentionally omit the `max_tokens` field. In the standard
// OpenAI API, `max_tokens` limits generation output. However, some versions of Ollama
// have a bug where they misinterpret `max_tokens` as a hard limit on the *total context
// window* (prompt + generation). If we pass `max_tokens: 4096`, Ollama instantly drops
// large HTML prompts (e.g. 6000 tokens) and returns an empty string, completely ignoring
// any OLLAMA_NUM_CTX server limits.
type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	ResponseFormat *respFormat   `json:"response_format,omitempty"`
}

type respFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
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

	// NOTE ON response_format: We intentionally omit `ResponseFormat: {"type":"json_object"}`.
	// When using reasoning models (like Qwen3.6 or DeepSeek R1) in Ollama, the model often
	// emits `<think>` tags before the JSON. Ollama's strict JSON grammar engine sees the
	// `<think>` tag, instantly flags it as invalid JSON, and aborts the generation, returning
	// an empty string. Instead, we let the model format naturally and use cleanJSON() to extract it.
	req := chatRequest{
		Model: e.Model,
		Messages: []chatMessage{
			{Role: "user", Content: content},
		},
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
	slog.Debug("LLM extracted payload", "bytes", len(raw), "finish_reason", chatResp.Choices[0].FinishReason)

	if strings.TrimSpace(raw) == "" {
		return MenuExtractionResult{}, fmt.Errorf(
			"LLM returned an empty response (finish_reason: %q). This usually happens when the restaurant menu is too large and exceeds the model's context window.\n\n"+
				"If you are using Ollama locally, you can fix this by increasing its context window memory. Stop your current Ollama server and restart it with:\n"+
				"  OLLAMA_NUM_CTX=16384 ollama serve",
			chatResp.Choices[0].FinishReason,
		)
	}
	cleaned := cleanJSON(raw)
	var result MenuExtractionResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return MenuExtractionResult{}, fmt.Errorf("parse LLM JSON output: %w (raw: %.200s)", err, raw)
	}
	return result, nil
}

// cleanJSON strips markdown formatting blocks (e.g. ```json ... ```) from the LLM output.
func cleanJSON(s string) string {
	start := strings.Index(s, "```json")
	if start != -1 {
		s = s[start+7:]
		end := strings.LastIndex(s, "```")
		if end != -1 {
			s = s[:end]
		}
	} else if start := strings.Index(s, "```"); start != -1 {
		s = s[start+3:]
		end := strings.LastIndex(s, "```")
		if end != -1 {
			s = s[:end]
		}
	}
	return strings.TrimSpace(s)
}
