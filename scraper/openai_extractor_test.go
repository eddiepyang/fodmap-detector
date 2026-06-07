package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatExtractor_Extract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		
		resp := chatResponse{}
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}{
			Message: struct {
				Content string `json:"content"`
			}{
				Content: "```json\n{\"items\": [{\"dish\": \"pizza\", \"description\": \"cheese\", \"price\": \"10\"}]}\n```",
			},
			FinishReason: "stop",
		})
		
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := NewOpenAICompatExtractor(srv.URL, "gpt-test", "test-key")
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

		resp := chatResponse{}
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}{
			Message: struct {
				Content string `json:"content"`
			}{
				Content: "{\"items\": []}",
			},
			FinishReason: "stop",
		})
		
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ext := NewOpenAICompatExtractor(srv.URL, "gpt-test", "")
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

		ext := NewOpenAICompatExtractor(srv.URL, "gpt-test", "")
		_, err := ext.Extract(context.Background(), "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})

	t.Run("Empty response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := chatResponse{}
			resp.Choices = append(resp.Choices, struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				Message: struct {
					Content string `json:"content"`
				}{
					Content: "",
				},
				FinishReason: "length",
			})
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer srv.Close()

		ext := NewOpenAICompatExtractor(srv.URL, "gpt-test", "")
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

		ext := NewOpenAICompatExtractor(srv.URL, "gpt-test", "")
		_, err := ext.Extract(context.Background(), "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})
	
	t.Run("Invalid JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := chatResponse{}
			resp.Choices = append(resp.Choices, struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				Message: struct {
					Content string `json:"content"`
				}{
					Content: "{invalid json}",
				},
				FinishReason: "stop",
			})
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer srv.Close()

		ext := NewOpenAICompatExtractor(srv.URL, "gpt-test", "")
		_, err := ext.Extract(context.Background(), "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})
}

func TestCleanJSON(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"```json\n{\"a\":1}\n```", "{\"a\":1}"},
		{"```\n{\"a\":1}\n```", "{\"a\":1}"},
		{"{\"a\":1}", "{\"a\":1}"},
		{"  {\"a\":1}  ", "{\"a\":1}"},
	}
	
	for _, tc := range tests {
		got := cleanJSON(tc.input)
		if got != tc.want {
			t.Errorf("cleanJSON(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}
