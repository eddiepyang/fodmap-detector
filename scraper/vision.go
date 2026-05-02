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
)

// VisionTranscriber transcribes menu images using a local Ollama vision model.
// It sends the image as raw base64 and enforces structured JSON output via
// Ollama's Format field (JSON schema constraint).
type VisionTranscriber struct {
	ollamaURL string
	model     string
	logger    *slog.Logger
}

// NewVisionTranscriber creates a VisionTranscriber pointing at an Ollama server.
// Recommended models: "gemma3" (4B, fast) or "gemma3:12b" (more accurate).
func NewVisionTranscriber(ollamaURL, model string, logger *slog.Logger) *VisionTranscriber {
	if model == "" {
		model = "gemma3"
	}
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &VisionTranscriber{
		ollamaURL: strings.TrimRight(ollamaURL, "/"),
		model:     model,
		logger:    logger,
	}
}

// menuItemsSchema is the JSON Schema passed to Ollama to constrain output.
var menuItemsSchema = json.RawMessage(`{
	"type": "array",
	"items": {
		"type": "object",
		"properties": {
			"name":        {"type": "string"},
			"description": {"type": "string"},
			"price":       {"type": "string"},
			"category":    {"type": "string"},
			"ingredients": {"type": "array", "items": {"type": "string"}}
		},
		"required": ["name"]
	}
}`)

// ollamaMessage is the JSON payload for the Ollama /api/chat endpoint.
type ollamaMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"` // raw base64 strings (no data URI prefix)
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Format   json.RawMessage `json:"format"`
	Options  map[string]any  `json:"options,omitempty"`
	Stream   bool            `json:"stream"`
}

type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Done bool `json:"done"`
}

// TranscribeImage sends imageData (PNG/JPEG bytes) to Ollama and returns
// the structured list of menu items extracted by the vision model.
func (v *VisionTranscriber) TranscribeImage(ctx context.Context, imageData []byte) ([]MenuItem, error) {
	b64 := base64.StdEncoding.EncodeToString(imageData)

	payload := ollamaChatRequest{
		Model: v.model,
		Messages: []ollamaMessage{{
			Role: "user",
			Content: "You are a data extraction assistant. Extract all menu items from " +
				"this restaurant menu image. For each item include its name, description " +
				"(if visible), price (if visible), category (e.g. Appetizers, Entrees, " +
				"Desserts), and any listed ingredients. Return ONLY valid JSON matching " +
				"the provided schema. No other text.",
			Images: []string{b64},
		}},
		Format:  menuItemsSchema,
		Options: map[string]any{"temperature": 0},
		Stream:  false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling vision request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		v.ollamaURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ollama returned HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var chatResp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decoding ollama response: %w", err)
	}

	var items []MenuItem
	if err := json.Unmarshal([]byte(chatResp.Message.Content), &items); err != nil {
		// Attempt to extract JSON if the model wrapped it in extra text.
		content := chatResp.Message.Content
		start := strings.Index(content, "[")
		end := strings.LastIndex(content, "]")
		if start >= 0 && end > start {
			if err2 := json.Unmarshal([]byte(content[start:end+1]), &items); err2 != nil {
				return nil, fmt.Errorf("parsing menu items from vision output: %w (raw: %s)", err, content)
			}
		} else {
			return nil, fmt.Errorf("parsing menu items from vision output: %w", err)
		}
	}

	v.logger.Info("vision transcription complete",
		"model", v.model,
		"items_found", len(items),
	)
	return items, nil
}

// TranscribeText sends raw menu text (from PDF/HTML extraction fallback) through
// Ollama as a text-only structured extraction.
func (v *VisionTranscriber) TranscribeText(ctx context.Context, rawText string) ([]MenuItem, error) {
	// Truncate to avoid context length limits (roughly 6k tokens ≈ 24k chars).
	const maxChars = 24_000
	if len(rawText) > maxChars {
		rawText = rawText[:maxChars] + "\n[TRUNCATED]"
	}

	payload := ollamaChatRequest{
		Model: v.model,
		Messages: []ollamaMessage{{
			Role: "user",
			Content: "You are a data extraction assistant. The following is raw text " +
				"extracted from a restaurant menu. Extract all menu items. For each item " +
				"include its name, description (if present), price (if present), category " +
				"(e.g. Appetizers, Entrees, Desserts), and any listed ingredients. " +
				"Return ONLY valid JSON. No other text.\n\nMENU TEXT:\n" + rawText,
		}},
		Format:  menuItemsSchema,
		Options: map[string]any{"temperature": 0},
		Stream:  false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		v.ollamaURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama text request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var chatResp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}

	var items []MenuItem
	if err := json.Unmarshal([]byte(chatResp.Message.Content), &items); err != nil {
		return nil, fmt.Errorf("parsing text transcription output: %w", err)
	}
	return items, nil
}
