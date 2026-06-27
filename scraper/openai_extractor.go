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

//go:embed scrape-prompt-vision.txt
var menuVisionPrompt string

// OpenAICompatExtractor calls any OpenAI-compatible /chat/completions endpoint
// (Ollama, vLLM, OpenAI, LM Studio, Gemini's /v1beta/openai wrapper, etc.).
// BaseURL must include the version segment: e.g. "http://localhost:11434/v1" or
// "https://generativelanguage.googleapis.com/v1beta/openai".
type OpenAICompatExtractor struct {
	BaseURL         string
	Model           string
	APIKey          string
	ReasoningEffort string
	schema          json.RawMessage
	client          *http.Client
}

// NewOpenAICompatExtractor returns an extractor pointing at baseURL.
// The HTTP client timeout is intentionally generous (5 min) because local
// vision models processing large images can be slow.
func NewOpenAICompatExtractor(baseURL, model, apiKey, reasoningEffort string) (*OpenAICompatExtractor, error) {
	schema, err := menuExtractionSchema()
	if err != nil {
		return nil, fmt.Errorf("building menu schema: %w", err)
	}
	return &OpenAICompatExtractor{
		BaseURL:         baseURL,
		Model:           model,
		APIKey:          apiKey,
		ReasoningEffort: reasoningEffort,
		schema:          schema,
		client:          &http.Client{Timeout: 5 * time.Minute},
	}, nil
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

type jsonSchemaFormat struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type respFormat struct {
	Type       string            `json:"type"`
	JSONSchema *jsonSchemaFormat `json:"json_schema,omitempty"`
}

// chatRequest represents the OpenAI-compatible /chat/completions payload.
//
// NOTE ON max_tokens: Intentionally omitted. Some Ollama versions misinterpret
// max_tokens as a hard limit on the total context window (prompt + generation),
// instantly dropping large prompts and returning empty strings.
type chatRequest struct {
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	ResponseFormat  *respFormat   `json:"response_format,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
			Reasoning        string `json:"reasoning,omitempty"`
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
	return e.chatJSON(ctx, menuVisionPrompt, &dataURL)
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
		ResponseFormat: &respFormat{
			Type: "json_schema",
			JSONSchema: &jsonSchemaFormat{
				Name:   "menu_extraction",
				Strict: true,
				Schema: e.schema,
			},
		},
		ReasoningEffort: e.ReasoningEffort,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return MenuExtractionResult{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.BaseURL+"/chat/completions", bytes.NewReader(body))
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

	choice := chatResp.Choices[0]
	raw := choice.Message.Content

	// reasoning_content (Ollama, older vLLM) or reasoning (current vLLM/OpenAI spec).
	reasoning := choice.Message.ReasoningContent
	if reasoning == "" {
		reasoning = choice.Message.Reasoning
	}

	slog.Debug("LLM extracted payload",
		"bytes", len(raw),
		"reasoning_bytes", len(reasoning),
		"finish_reason", choice.FinishReason,
	)

	if strings.TrimSpace(raw) == "" {
		if reasoning != "" {
			return MenuExtractionResult{}, fmt.Errorf(
				"LLM returned reasoning but empty content (finish_reason: %q); "+
					"some models (e.g. Gemma family on Ollama) emit all output in the reasoning channel — "+
					"try --llm-reasoning-effort=low or restart Ollama with --reasoning-parser deepseek_r1",
				choice.FinishReason,
			)
		}
		return MenuExtractionResult{}, fmt.Errorf(
			"LLM returned an empty response (finish_reason: %q). This usually happens when the restaurant menu is too large and exceeds the model's context window.\n\n"+
				"If you are using Ollama locally, you can fix this by increasing its context window memory. Stop your current Ollama server and restart it with:\n"+
				"  OLLAMA_NUM_CTX=16384 ollama serve --reasoning-parser deepseek_r1",
			choice.FinishReason,
		)
	}

	var payload llmMenuPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return MenuExtractionResult{}, fmt.Errorf("parse LLM JSON output: %w (raw: %.200s)", err, raw)
	}
	return MenuExtractionResult{
		RestaurantName: payload.RestaurantName,
		City:           payload.City,
		State:          payload.State,
		Items:          payload.Items,
	}, nil
}
