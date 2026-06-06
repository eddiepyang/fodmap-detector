package scraper

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/genai"
)

// GeminiExtractor calls the Google Gemini API via the google.golang.org/genai
// SDK (already in go.mod). It implements the Extractor interface.
type GeminiExtractor struct {
	Client *genai.Client
	Model  string
}

// NewGeminiExtractor returns an extractor using the given genai client.
func NewGeminiExtractor(client *genai.Client, model string) *GeminiExtractor {
	return &GeminiExtractor{Client: client, Model: model}
}

// Extract sends pageText to Gemini with JSON-mode response and parses the
// returned MenuExtractionResult.
func (e *GeminiExtractor) Extract(ctx context.Context, pageText string) (MenuExtractionResult, error) {
	pageText = truncateText(pageText, MaxInputChars)
	prompt := menuPrompt + pageText

	resp, err := e.Client.Models.GenerateContent(ctx, e.Model,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			ResponseMIMEType: "application/json",
		},
	)
	if err != nil {
		return MenuExtractionResult{}, fmt.Errorf("gemini generate: %w", err)
	}

	raw := resp.Text()
	var result MenuExtractionResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return MenuExtractionResult{}, fmt.Errorf("parse gemini JSON output: %w (raw: %.200s)", err, raw)
	}
	return result, nil
}
