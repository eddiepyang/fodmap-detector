// Package integration contains end-to-end tests for the HTTP server.
// They spin up the real handler mux via httptest and exercise routes
// without making live Gemini API calls (a stubAnalyzer is injected instead).
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"fodmap/search"
	"fodmap/server"
)



// stubSearcher is a test double for server.Searcher.
type stubSearcher struct {
	result           search.SearchResult
	reviewResult     search.SearchReviews
	fodmapResult     search.FodmapResult
	fodmapCertainty  float64
	err              error
	lastReviewFilter search.SearchFilter
}

func (s *stubSearcher) GetBusinesses(_ context.Context, _ string, _ int, _ search.SearchFilter) (search.SearchResult, error) {
	return s.result, s.err
}

func (s *stubSearcher) SearchFodmap(_ context.Context, _ string) (search.FodmapResult, float64, error) {
	return s.fodmapResult, s.fodmapCertainty, s.err
}

func (s *stubSearcher) GetReviews(_ context.Context, _ string, _ int, filter search.SearchFilter) (search.SearchReviews, error) {
	s.lastReviewFilter = filter
	return s.reviewResult, s.err
}

// newMux returns the handler mux used by the server, wired to the stub analyzer.
// Pass nil for searcher to leave the search endpoint disabled (returns 503).
func newMux(t *testing.T, searcher server.Searcher) http.Handler {
	t.Helper()
	srv := server.NewServer(searcher, 0)
	return srv.Handler()
}



// --- /reviews ---

func TestReviewsHandler_MissingBusinessID(t *testing.T) {
	mux := newMux(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/reviews", nil)

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestReviewsHandler_ArchiveMissing(t *testing.T) {
	// In the integration test environment the archive is not present, so the
	// handler should respond with 500 and not panic.
	mux := newMux(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/reviews?business_id=biz1", nil)

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// --- /search ---

func TestSearchHandler_NoSearcherConfigured(t *testing.T) {
	mux := newMux(t, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/searchBusiness/tacos", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestSearchHandler_MissingQuery(t *testing.T) {
	mux := newMux(t, &stubSearcher{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/searchBusiness/", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSearchHandler_ReturnsBusinesses(t *testing.T) {
	stub := &stubSearcher{
		result: search.SearchResult{Businesses: []search.BusinessResult{
			{ID: "biz1", Name: "Biz One"},
			{ID: "biz2", Name: "Biz Two"},
		}},
	}
	mux := newMux(t, stub)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/searchBusiness/tacos", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Businesses []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"businesses"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Businesses) != 2 {
		t.Errorf("got %d businesses, want 2", len(resp.Businesses))
	}
	if resp.Businesses[0].Name != "Biz One" {
		t.Errorf("got name %q, want %q", resp.Businesses[0].Name, "Biz One")
	}
}

func TestSearchHandler_InvalidLimit(t *testing.T) {
	mux := newMux(t, &stubSearcher{result: search.SearchResult{Businesses: []search.BusinessResult{{ID: "biz1", Name: "Biz One"}}}})

	cases := []string{"0", "-5", "abc"}
	for _, limit := range cases {
		t.Run("limit="+limit, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/searchBusiness/tacos?limit="+limit, nil))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("limit=%q: status = %d, want %d", limit, rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestSearchHandler_EmptyResultIsNotNull(t *testing.T) {
	mux := newMux(t, &stubSearcher{result: search.SearchResult{}})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/searchBusiness/noresults", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	// Ensure response is {"businesses":[]} not {"businesses":null}
	body := rec.Body.String()
	if body == "" {
		t.Fatal("empty response body")
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if string(resp["businesses"]) == "null" {
		t.Error("businesses should be [] not null")
	}
}

// --- /searchReview ---

func TestSearchReviewHandler_ParsesBusinessID(t *testing.T) {
	stub := &stubSearcher{
		reviewResult: search.SearchReviews{BusinessReviews: []search.RankedReview{}},
	}
	mux := newMux(t, stub)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/searchReview/pad%20thai?business_id=my-biz-123", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if stub.lastReviewFilter.BusinessID != "my-biz-123" {
		t.Errorf("got business_id %q, want %q", stub.lastReviewFilter.BusinessID, "my-biz-123")
	}
}

// --- /searchFodmap ---

func TestFodmapHandler_NoSearcherConfigured(t *testing.T) {
	mux := newMux(t, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/searchFodmap/garlic", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestFodmapHandler_MissingIngredient(t *testing.T) {
	mux := newMux(t, &stubSearcher{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/searchFodmap/", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestFodmapHandler_ReturnsIngredient(t *testing.T) {
	stub := &stubSearcher{
		fodmapResult: search.FodmapResult{
			Ingredient: "garlic",
			Level:      "high",
			Groups:     []string{"fructans"},
			Notes:      "Keep away",
		},
		fodmapCertainty: 0.95,
	}
	mux := newMux(t, stub)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/searchFodmap/garlic", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp struct {
		Ingredient  string   `json:"ingredient"`
		Level       string   `json:"level"`
		Groups      []string `json:"groups"`
		Notes       string   `json:"notes"`
		Certainty   float64  `json:"certainty"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Ingredient != "garlic" {
		t.Errorf("got ingredient %q, want %q", resp.Ingredient, "garlic")
	}
	if len(resp.Groups) != 1 || resp.Groups[0] != "fructans" {
		t.Errorf("got groups %v, want [fructans]", resp.Groups)
	}
}
