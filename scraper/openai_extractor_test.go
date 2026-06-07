package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestExtractor builds an extractor pointed at srv.URL+"/v1" so the appended
// "/chat/completions" suffix produces the correct path that httptest expects.
func newTestExtractor(t *testing.T, baseURL, model, apiKey string) *OpenAICompatExtractor {
	t.Helper()
	ex, err := NewOpenAICompatExtractor(baseURL+"/v1", model, apiKey, "none")
	if err != nil {
		t.Fatalf("NewOpenAICompatExtractor: %v", err)
	}
	return ex
}

func makeChoiceResp(content, reasoningContent, reasoning, finishReason string) chatResponse {
	r := chatResponse{}
	r.Choices = append(r.Choices, struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
			Reasoning        string `json:"reasoning,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	}{
		Message: struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
			Reasoning        string `json:"reasoning,omitempty"`
		}{
			Content:          content,
			ReasoningContent: reasoningContent,
			Reasoning:        reasoning,
		},
		FinishReason: finishReason,
	})
	return r
}

func TestOpenAICompatExtractor_Extract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		resp := makeChoiceResp(`{"restaurant_name":"","items":[{"dish":"pizza","description":"cheese","stated_ingredients":[],"has_full_ingredients":false}]}`, "", "", "stop")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "test-key")
	res, err := ext.Extract(context.Background(), "pizza menu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(res.Items))
	}
	if res.Items[0].DishName != "pizza" {
		t.Errorf("expected pizza, got %s", res.Items[0].DishName)
	}
}

func TestOpenAICompatExtractor_RequestPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.ResponseFormat == nil {
			t.Fatal("ResponseFormat is nil")
		}
		if req.ResponseFormat.Type != "json_schema" {
			t.Errorf("response_format.type = %q; want json_schema", req.ResponseFormat.Type)
		}
		if req.ResponseFormat.JSONSchema == nil {
			t.Fatal("ResponseFormat.JSONSchema is nil")
		}
		if !req.ResponseFormat.JSONSchema.Strict {
			t.Error("json_schema.strict should be true")
		}
		// Schema should contain our key fields.
		schemaStr := string(req.ResponseFormat.JSONSchema.Schema)
		for _, field := range []string{"restaurant_name", "items"} {
			if !strings.Contains(schemaStr, field) {
				t.Errorf("schema missing field %q", field)
			}
		}
		if req.ReasoningEffort != "none" {
			t.Errorf("reasoning_effort = %q; want none", req.ReasoningEffort)
		}
		resp := makeChoiceResp(`{"restaurant_name":"","items":[]}`, "", "", "stop")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	_, err := ext.Extract(context.Background(), "menu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAICompatExtractor_ReasoningContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := makeChoiceResp(
			`{"restaurant_name":"Joe's","items":[{"dish":"pizza","description":"cheese","stated_ingredients":[],"has_full_ingredients":false}]}`,
			"<think>Let me extract the menu...</think>",
			"",
			"stop",
		)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	res, err := ext.Extract(context.Background(), "pizza menu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RestaurantName != "Joe's" {
		t.Errorf("expected Joe's, got %q", res.RestaurantName)
	}
	if len(res.Items) != 1 || res.Items[0].DishName != "pizza" {
		t.Errorf("unexpected items: %+v", res.Items)
	}
}

func TestOpenAICompatExtractor_ReasoningFieldFallback(t *testing.T) {
	// vLLM uses "reasoning" not "reasoning_content" after their rename.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := makeChoiceResp(
			`{"restaurant_name":"","items":[{"dish":"burger","description":"beef","stated_ingredients":["beef"],"has_full_ingredients":true}]}`,
			"",
			"I thought about this carefully...",
			"stop",
		)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	res, err := ext.Extract(context.Background(), "burger menu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].DishName != "burger" {
		t.Errorf("unexpected items: %+v", res.Items)
	}
}

func TestOpenAICompatExtractor_EmptyContentReasoningOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := makeChoiceResp("", "all my thoughts here", "", "stop")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	_, err := ext.Extract(context.Background(), "menu")
	if err == nil {
		t.Fatal("expected error for empty content with reasoning")
	}
	if !strings.Contains(err.Error(), "--llm-reasoning-effort") {
		t.Errorf("error should mention --llm-reasoning-effort, got: %v", err)
	}
}

func TestOpenAICompatExtractor_ExtractImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode err: %v", err)
		}
		if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 {
			t.Fatalf("unexpected message format")
		}
		if req.Messages[0].Content[1].Type != "image_url" || req.Messages[0].Content[1].ImageURL.URL != "data:image/png;base64,YWJj" {
			t.Errorf("unexpected image url")
		}
		resp := makeChoiceResp(`{"restaurant_name":"","items":[]}`, "", "", "stop")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := newTestExtractor(t, srv.URL, "gpt-test", "")
	res, err := ext.ExtractImage(context.Background(), []byte("abc"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Items) != 0 {
		t.Errorf("expected 0 items")
	}
}

func TestOpenAICompatExtractor_Errors(t *testing.T) {
	t.Run("HTTP 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		ext := newTestExtractor(t, srv.URL, "gpt-test", "")
		_, err := ext.Extract(context.Background(), "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})

	t.Run("Empty response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := makeChoiceResp("", "", "", "length")
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer srv.Close()

		ext := newTestExtractor(t, srv.URL, "gpt-test", "")
		_, err := ext.Extract(context.Background(), "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})

	t.Run("No choices", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(chatResponse{})
		}))
		defer srv.Close()

		ext := newTestExtractor(t, srv.URL, "gpt-test", "")
		_, err := ext.Extract(context.Background(), "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := makeChoiceResp("{invalid json}", "", "", "stop")
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer srv.Close()

		ext := newTestExtractor(t, srv.URL, "gpt-test", "")
		_, err := ext.Extract(context.Background(), "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})
}
