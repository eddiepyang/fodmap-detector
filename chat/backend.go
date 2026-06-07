package chat

import (
	"context"
	"encoding/json"
)

// ToolDeclaration describes a tool the model can call, provider-agnostic.
type ToolDeclaration struct {
	Name        string
	Description string
	Parameters  json.RawMessage // standard JSON Schema
}

// FunctionCall represents a model's request to call a tool.
type FunctionCall struct {
	Name             string
	Args             map[string]any
	ThoughtSignature string // Gemini thought signature
}

// FunctionResult pairs a tool name with its result.
type FunctionResult struct {
	Name   string
	Result map[string]any
}

// Message is a provider-agnostic chat message.
type Message struct {
	Role            string           // "user", "model", "tool"
	Text            string           // text content (user or model)
	FunctionCalls   []FunctionCall   // model requesting tool calls
	FunctionResults []FunctionResult // tool responses being fed back
}

// GenerateOpts bundles inputs for a single Generate call.
type GenerateOpts struct {
	SystemPrompt string
	History      []Message
	Tools        []ToolDeclaration
	OnText       func(string)   // streaming callback (nil = no streaming)
	OnToolCall   func([]string) // tool-call notification (nil = no notification)
}

// ChatBackend abstracts the LLM provider for tool-call capable chat.
type ChatBackend interface {
	// Generate sends a conversation (system prompt + history)
	// and returns the model's response. The backend handles streaming internally
	// and calls OnText for each text chunk if non-nil.
	Generate(ctx context.Context, opts GenerateOpts) (Message, error)
}
