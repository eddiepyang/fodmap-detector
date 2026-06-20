package chat

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

//go:embed dietary-profile-prompt.txt
var dietaryProfilePrompt string

// GenerateDietaryProfile calls Gemini to extract a structured dietary profile from user input.
func GenerateDietaryProfile(ctx context.Context, client *genai.Client, model string, userInput string) (json.RawMessage, error) {
	if model == "" {
		model = ScreenGeminiModel
	}

	prompt := fmt.Sprintf(dietaryProfilePrompt, userInput)
	resp, err := client.Models.GenerateContent(ctx, model, genai.Text(prompt), nil)
	if err != nil {
		return nil, fmt.Errorf("generate dietary profile: %w", err)
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty response from model")
	}

	var out strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" {
			out.WriteString(part.Text)
		}
	}
	text := strings.TrimSpace(out.String())

	// Strip potential markdown code blocks
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	if !json.Valid([]byte(text)) {
		return nil, fmt.Errorf("model returned invalid JSON: %s", text)
	}

	return json.RawMessage(text), nil
}
