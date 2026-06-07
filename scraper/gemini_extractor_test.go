package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/genai"
)

func TestGeminiExtractor_Extract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Provide a minimal valid response for the genai SDK to parse
		// The genai SDK expects a GenerateContentResponse structure
		fmt := `{
			"candidates": [
				{
					"content": {
						"parts": [
							{"text": "{\"items\": [{\"dish\": \"pizza\", \"description\": \"cheese\", \"price\": \"10\"}]}"}
						]
					}
				}
			]
		}`
		_, _ = w.Write([]byte(fmt))
	}))
	defer srv.Close()

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		HTTPOptions: genai.HTTPOptions{
			BaseURL: srv.URL,
		},
		APIKey:  "test-key",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	ext := NewGeminiExtractor(client, "gemini-1.5-flash")
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

func TestGeminiExtractor_Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client, _ := genai.NewClient(context.Background(), &genai.ClientConfig{
		HTTPOptions: genai.HTTPOptions{
			BaseURL: srv.URL,
		},
		APIKey:  "test-key",
	})
	ext := NewGeminiExtractor(client, "gemini-1.5-flash")
	_, err := ext.Extract(context.Background(), "pizza menu")
	if err == nil {
		t.Errorf("expected error")
	}
}
