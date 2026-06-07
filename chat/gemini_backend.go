package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/genai"
)

// GeminiBackend implements ChatBackend using the official genai SDK.
type GeminiBackend struct {
	client *genai.Client
	model  string
}

// NewGeminiBackend creates a new GeminiBackend wrapping an existing genai client.
func NewGeminiBackend(client *genai.Client, model string) *GeminiBackend {
	return &GeminiBackend{
		client: client,
		model:  model,
	}
}

// Generate implements ChatBackend.
func (b *GeminiBackend) Generate(ctx context.Context, opts GenerateOpts) (Message, error) {
	// 1. Build Config (system prompt + tools)
	var cfg *genai.GenerateContentConfig
	if opts.SystemPrompt != "" || len(opts.Tools) > 0 {
		cfg = &genai.GenerateContentConfig{}
		if opts.SystemPrompt != "" {
			cfg.SystemInstruction = &genai.Content{
				Parts: []*genai.Part{{Text: opts.SystemPrompt}},
			}
		}

		if len(opts.Tools) > 0 {
			tool := &genai.Tool{}
			for _, td := range opts.Tools {
				decl := &genai.FunctionDeclaration{
					Name:        td.Name,
					Description: td.Description,
				}
				if len(td.Parameters) > 0 {
					var schema genai.Schema
					if err := json.Unmarshal(td.Parameters, &schema); err != nil {
						return Message{}, fmt.Errorf("parsing tool %q parameters: %w", td.Name, err)
					}
					decl.Parameters = &schema
				}
				tool.FunctionDeclarations = append(tool.FunctionDeclarations, decl)
			}
			cfg.Tools = []*genai.Tool{tool}
		}
	}

	// 2. Build History
	var history []*genai.Content
	for _, m := range opts.History {
		content := &genai.Content{Role: m.Role}
		if m.Text != "" {
			content.Parts = append(content.Parts, &genai.Part{Text: m.Text})
		}
		for _, fc := range m.FunctionCalls {
			part := &genai.Part{
				FunctionCall: &genai.FunctionCall{
					Name: fc.Name,
					Args: fc.Args,
				},
			}
			if fc.ThoughtSignature != "" {
				part.ThoughtSignature = []byte(fc.ThoughtSignature)
			}
			content.Parts = append(content.Parts, part)
		}
		for _, fr := range m.FunctionResults {
			content.Parts = append(content.Parts, genai.NewPartFromFunctionResponse(fr.Name, fr.Result))
		}
		history = append(history, content)
	}

	// 3. Call GenerateContentStream
	var fullText string
	var outCalls []FunctionCall

	for resp, err := range b.client.Models.GenerateContentStream(ctx, b.model, history, cfg) {
		if err != nil {
			return Message{}, fmt.Errorf("stream error: %w", err)
		}
		if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
			continue
		}

		for _, part := range resp.Candidates[0].Content.Parts {
			if part.Text != "" {
				fullText += part.Text
				if opts.OnText != nil {
					opts.OnText(part.Text)
				}
			}
			if part.FunctionCall != nil {
				fc := FunctionCall{
					Name: part.FunctionCall.Name,
					Args: part.FunctionCall.Args,
				}
				if len(part.ThoughtSignature) > 0 {
					fc.ThoughtSignature = string(part.ThoughtSignature)
				}
				outCalls = append(outCalls, fc)

				if opts.OnToolCall != nil {
					opts.OnToolCall([]string{fc.Name})
				} else if opts.OnText != nil {
					opts.OnText(fmt.Sprintf("\n[Tool Call] %s\n", fc.Name))
				}
			}
		}
	}

	return Message{
		Role:          "model",
		Text:          fullText,
		FunctionCalls: outCalls,
	}, nil
}
