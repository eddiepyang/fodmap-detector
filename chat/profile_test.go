package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/genai"
)

func TestGenerateDietaryProfile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt := "{ \"candidates\": [ { \"content\": { \"parts\": [ {\"text\": \"```json\\n{\\\"intolerances\\\": [\\\"lactose\\\"]}\\n```\"} ] } } ] }"
		_, _ = w.Write([]byte(fmt))
	}))
	defer srv.Close()

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		HTTPOptions: genai.HTTPOptions{
			BaseURL: srv.URL,
		},
		APIKey: "test-key",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	b, err := GenerateDietaryProfile(context.Background(), client, "", "i am lactose intolerant")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(b) != `{"intolerances": ["lactose"]}` {
		t.Errorf("unexpected output: %s", string(b))
	}
}

func TestGenerateDietaryProfile_Errors(t *testing.T) {
	t.Run("HTTP 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		client, _ := genai.NewClient(context.Background(), &genai.ClientConfig{
			HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL},
			APIKey:      "test-key",
		})

		_, err := GenerateDietaryProfile(context.Background(), client, "", "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})

	t.Run("Empty Response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"candidates":[]}`))
		}))
		defer srv.Close()

		client, _ := genai.NewClient(context.Background(), &genai.ClientConfig{
			HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL},
			APIKey:      "test-key",
		})

		_, err := GenerateDietaryProfile(context.Background(), client, "", "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"invalid json"}]}}]}`))
		}))
		defer srv.Close()

		client, _ := genai.NewClient(context.Background(), &genai.ClientConfig{
			HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL},
			APIKey:      "test-key",
		})

		_, err := GenerateDietaryProfile(context.Background(), client, "", "test")
		if err == nil {
			t.Errorf("expected error")
		}
	})
}
