package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchInferredEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/menu" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	t.Run("Valid", func(t *testing.T) {
		jsonPayload := `{"url": "` + srv.URL + `/api/menu", "method": "GET"}`
		b, err := FetchInferredEndpoint(context.Background(), jsonPayload, srv.URL)
		if err == nil || !strings.Contains(err.Error(), "SSRF guard rejected") {
			t.Fatalf("expected SSRF error, got: %v", err)
		}
		if len(b) != 0 {
			t.Errorf("expected empty body")
		}
	})

	t.Run("MismatchHost", func(t *testing.T) {
		jsonPayload := `{"url": "http://other.com/api", "method": "GET"}`
		_, err := FetchInferredEndpoint(context.Background(), jsonPayload, srv.URL)
		if err == nil {
			t.Errorf("expected error")
		}
	})

	t.Run("404", func(t *testing.T) {
		jsonPayload := `{"url": "` + srv.URL + `/missing", "method": "GET"}`
		_, err := FetchInferredEndpoint(context.Background(), jsonPayload, srv.URL)
		if err == nil {
			t.Errorf("expected error")
		}
	})
}
