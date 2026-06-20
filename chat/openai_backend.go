package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenAICompatBackend implements ChatBackend against an OpenAI-compatible
// API (e.g. Ollama's OpenAI shim, vLLM, or OpenAI itself).
type OpenAICompatBackend struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

// NewOpenAICompatBackend creates a backend targeting the given OpenAI-compatible
// API base URL.
func NewOpenAICompatBackend(baseURL, model, apiKey string) *OpenAICompatBackend {
	return &OpenAICompatBackend{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		model:   model,
		apiKey:  apiKey,
		client:  &http.Client{},
	}
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Name       string        `json:"name,omitempty"`
}

type oaiToolCall struct {
	Index    int         `json:"index,omitempty"`
	ID       string      `json:"id,omitempty"`
	Type     string      `json:"type,omitempty"`
	Function oaiFunction `json:"function"`
}

type oaiFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type oaiTool struct {
	Type     string         `json:"type"`
	Function oaiToolFuncDef `json:"function"`
}

type oaiToolFuncDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type oaiRequest struct {
	Model    string       `json:"model"`
	Messages []oaiMessage `json:"messages"`
	Tools    []oaiTool    `json:"tools,omitempty"`
	Stream   bool         `json:"stream"`
}

type oaiResponse struct {
	Choices []struct {
		Message struct {
			Content   string        `json:"content"`
			ToolCalls []oaiToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
	} `json:"choices"`
}

type oaiStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content   string        `json:"content"`
			ToolCalls []oaiToolCall `json:"tool_calls,omitempty"`
		} `json:"delta"`
	} `json:"choices"`
}

// Generate sends a chat completion request and returns the assistant's reply.
func (b *OpenAICompatBackend) Generate(ctx context.Context, opts GenerateOpts) (Message, error) {
	var msgs []oaiMessage
	if opts.SystemPrompt != "" {
		msgs = append(msgs, oaiMessage{Role: "system", Content: opts.SystemPrompt})
	}

	for _, h := range opts.History {
		role := h.Role
		if role == "model" {
			role = "assistant"
		}

		if len(h.FunctionResults) > 0 {
			for _, fr := range h.FunctionResults {
				resBytes, _ := json.Marshal(fr.Result)
				msgs = append(msgs, oaiMessage{
					Role:       "tool",
					Content:    string(resBytes),
					Name:       fr.Name,
					ToolCallID: fr.Name + "_call",
				})
			}
			continue
		}

		msg := oaiMessage{Role: role, Content: h.Text}
		for i, fc := range h.FunctionCalls {
			argBytes, _ := json.Marshal(fc.Args)
			msg.ToolCalls = append(msg.ToolCalls, oaiToolCall{
				ID:   fmt.Sprintf("%s_call_%d", fc.Name, i),
				Type: "function",
				Function: oaiFunction{
					Name:      fc.Name,
					Arguments: string(argBytes),
				},
			})
		}
		msgs = append(msgs, msg)
	}

	var tools []oaiTool
	for _, td := range opts.Tools {
		tools = append(tools, oaiTool{
			Type:     "function",
			Function: oaiToolFuncDef(td),
		})
	}

	reqBody := oaiRequest{
		Model:    b.model,
		Messages: msgs,
		Tools:    tools,
		Stream:   opts.OnText != nil,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return Message{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", b.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.apiKey)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		out, _ := io.ReadAll(resp.Body)
		return Message{}, fmt.Errorf("openai error %d: %s", resp.StatusCode, out)
	}

	var fullText string
	var toolCallsMap = make(map[int]*oaiToolCall)

	if reqBody.Stream {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || line == "data: [DONE]" {
				continue
			}
			if strings.HasPrefix(line, "data: ") {
				line = strings.TrimPrefix(line, "data: ")
				var chunk oaiStreamResponse
				if err := json.Unmarshal([]byte(line), &chunk); err != nil {
					continue
				}
				if len(chunk.Choices) > 0 {
					delta := chunk.Choices[0].Delta
					if delta.Content != "" {
						fullText += delta.Content
						if opts.OnText != nil {
							opts.OnText(delta.Content)
						}
					}
					for _, tc := range delta.ToolCalls {
						if toolCallsMap[tc.Index] == nil {
							toolCallsMap[tc.Index] = &oaiToolCall{
								ID: tc.ID, Type: tc.Type, Function: oaiFunction{Name: tc.Function.Name},
							}
						}
						toolCallsMap[tc.Index].Function.Arguments += tc.Function.Arguments
					}
				}
			}
		}
	} else {
		var oaiResp oaiResponse
		if err := json.NewDecoder(resp.Body).Decode(&oaiResp); err != nil {
			return Message{}, err
		}
		if len(oaiResp.Choices) > 0 {
			fullText = oaiResp.Choices[0].Message.Content
			if opts.OnText != nil && fullText != "" {
				opts.OnText(fullText)
			}
			for i, tc := range oaiResp.Choices[0].Message.ToolCalls {
				tcCopy := tc
				toolCallsMap[i] = &tcCopy
			}
		}
	}

	msg := Message{Role: "model", Text: fullText}
	maxIdx := -1
	for k := range toolCallsMap {
		if k > maxIdx {
			maxIdx = k
		}
	}
	for i := 0; i <= maxIdx; i++ {
		if tc, ok := toolCallsMap[i]; ok {
			var args map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			msg.FunctionCalls = append(msg.FunctionCalls, FunctionCall{
				Name: tc.Function.Name,
				Args: args,
			})
			if opts.OnToolCall != nil {
				opts.OnToolCall([]string{tc.Function.Name})
			}
		}
	}

	return msg, nil
}
