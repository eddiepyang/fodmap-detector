package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"fodmap/data"
	"fodmap/data/schemas"
	"fodmap/search"
)

// handlersTestSearcher is a configurable mock Searcher for handler tests.
type handlersTestSearcher struct {
	businessResult search.SearchResult
	businessErr    error
	reviewResult   search.SearchReviews
	reviewErr      error
	fodmapResult   search.FodmapResult
	fodmapCert     float64
	fodmapErr      error
}

func (m *handlersTestSearcher) GetBusinesses(_ context.Context, _ string, _ int, _ search.SearchFilter) (search.SearchResult, error) {
	return m.businessResult, m.businessErr
}

func (m *handlersTestSearcher) GetReviews(_ context.Context, _ string, _ int, _ search.SearchFilter) (search.SearchReviews, error) {
	return m.reviewResult, m.reviewErr
}

func (m *handlersTestSearcher) SearchFodmap(_ context.Context, _ string) (search.FodmapResult, float64, error) {
	return m.fodmapResult, m.fodmapCert, m.fodmapErr
}

func (m *handlersTestSearcher) EnsureSchema(ctx context.Context) error {
	return nil
}

func (m *handlersTestSearcher) EnsureFodmapSchema(ctx context.Context) error {
	return nil
}

func (m *handlersTestSearcher) BatchUpsertFodmap(_ context.Context, _ map[string]data.FodmapEntry) error {
	return nil
}

// ---- reviewsHandler (backed by data package, harder to mock fully) ----
// reviewsHandler calls data.GetReviewsByBusiness directly, so we only test
// the "missing business_id" path which doesn't touch the filesystem.

func TestReviewsHandler_MissingBusinessID(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/reviews", nil)
	rec := httptest.NewRecorder()

	s.reviewsHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ---- getBusinessesHandler ----

func TestGetBusinessesHandler_Success(t *testing.T) {
	mock := &handlersTestSearcher{
		businessResult: search.SearchResult{
			Businesses: []search.BusinessResult{
				{ID: "biz1", Name: "Test Restaurant", City: "NYC", State: "NY", Score: 0.95},
			},
		},
	}
	s := &Server{searcher: mock}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchBusiness/{query...}", s.getBusinessesHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchBusiness/pizza", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body struct {
		Businesses []struct {
			ID    string  `json:"id"`
			Name  string  `json:"name"`
			City  string  `json:"city"`
			State string  `json:"state"`
			Score float64 `json:"score"`
		} `json:"businesses"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Businesses) != 1 {
		t.Fatalf("got %d businesses, want 1", len(body.Businesses))
	}
	if body.Businesses[0].Name != "Test Restaurant" {
		t.Errorf("name = %q, want %q", body.Businesses[0].Name, "Test Restaurant")
	}
}

func TestGetBusinessesHandler_NoSearcher(t *testing.T) {
	s := &Server{searcher: nil}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchBusiness/{query...}", s.getBusinessesHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchBusiness/pizza", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestGetBusinessesHandler_EmptyQuery(t *testing.T) {
	mock := &handlersTestSearcher{}
	s := &Server{searcher: mock}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchBusiness/{query...}", s.getBusinessesHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchBusiness/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGetBusinessesHandler_InvalidLimit(t *testing.T) {
	mock := &handlersTestSearcher{}
	s := &Server{searcher: mock}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchBusiness/{query...}", s.getBusinessesHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchBusiness/pizza?limit=abc", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGetBusinessesHandler_NegativeLimit(t *testing.T) {
	mock := &handlersTestSearcher{}
	s := &Server{searcher: mock}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchBusiness/{query...}", s.getBusinessesHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchBusiness/pizza?limit=-1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGetBusinessesHandler_EmptyResults(t *testing.T) {
	mock := &handlersTestSearcher{
		businessResult: search.SearchResult{Businesses: nil},
	}
	s := &Server{searcher: mock}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchBusiness/{query...}", s.getBusinessesHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchBusiness/nonexistent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Businesses []any `json:"businesses"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Must be [] not null
	if body.Businesses == nil {
		t.Error("businesses is null, want empty array")
	}
}

func TestGetBusinessesHandler_SearchError(t *testing.T) {
	mock := &handlersTestSearcher{businessErr: errors.New("search down")}
	s := &Server{searcher: mock}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchBusiness/{query...}", s.getBusinessesHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchBusiness/pizza", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestGetBusinessesHandler_WithFilters(t *testing.T) {
	mock := &handlersTestSearcher{
		businessResult: search.SearchResult{
			Businesses: []search.BusinessResult{{ID: "biz1", Name: "Biz"}},
		},
	}
	s := &Server{searcher: mock}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchBusiness/{query...}", s.getBusinessesHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchBusiness/tacos?category=Mexican&city=Austin&state=TX&limit=5", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// ---- getReviewsHandler ----

func TestGetReviewsHandler_Success(t *testing.T) {
	mock := &handlersTestSearcher{
		reviewResult: search.SearchReviews{
			BusinessReviews: []search.RankedReview{
				{
					Score: 0.9,
					Review: search.IndexItem{
						BusinessName: "Pizza Place",
						City:         "NYC",
						State:        "NY",
						Review:       schemas.Review{BusinessID: "biz1", Text: "Great pizza"},
					},
				},
			},
		},
	}
	s := &Server{searcher: mock}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchReview/{query...}", s.getReviewsHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchReview/pizza", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body struct {
		Reviews []struct {
			Text         string  `json:"text"`
			BusinessID   string  `json:"business_id"`
			BusinessName string  `json:"business_name"`
			Score        float64 `json:"score"`
		} `json:"reviews"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Reviews) != 1 {
		t.Fatalf("got %d reviews, want 1", len(body.Reviews))
	}
	if body.Reviews[0].Text != "Great pizza" {
		t.Errorf("text = %q, want %q", body.Reviews[0].Text, "Great pizza")
	}
}

func TestGetReviewsHandler_NoSearcher(t *testing.T) {
	s := &Server{searcher: nil}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchReview/{query...}", s.getReviewsHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchReview/pizza", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestGetReviewsHandler_EmptyQuery(t *testing.T) {
	s := &Server{searcher: &handlersTestSearcher{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchReview/{query...}", s.getReviewsHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchReview/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGetReviewsHandler_InvalidLimit(t *testing.T) {
	s := &Server{searcher: &handlersTestSearcher{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchReview/{query...}", s.getReviewsHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchReview/pizza?limit=xyz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGetReviewsHandler_EmptyResults(t *testing.T) {
	mock := &handlersTestSearcher{
		reviewResult: search.SearchReviews{BusinessReviews: nil},
	}
	s := &Server{searcher: mock}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchReview/{query...}", s.getReviewsHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchReview/nonexistent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Reviews []any `json:"reviews"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Reviews == nil {
		t.Error("reviews is null, want empty array")
	}
}

func TestGetReviewsHandler_SearchError(t *testing.T) {
	mock := &handlersTestSearcher{reviewErr: errors.New("search down")}
	s := &Server{searcher: mock}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchReview/{query...}", s.getReviewsHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchReview/pizza", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ---- getFodmapHandler ----

func TestGetFodmapHandler_Success(t *testing.T) {
	mock := &handlersTestSearcher{
		fodmapResult: search.FodmapResult{
			Ingredient: "garlic",
			Level:      "high",
			Groups:     []string{"fructans"},
			Notes:      "Even small amounts are high FODMAP",
		},
		fodmapCert: 0.95,
	}
	s := &Server{searcher: mock}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchFodmap/{ingredient...}", s.getFodmapHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchFodmap/garlic", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body struct {
		Ingredient string   `json:"ingredient"`
		Level      string   `json:"level"`
		Groups     []string `json:"groups"`
		Notes      string   `json:"notes"`
		Certainty  float64  `json:"certainty"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Ingredient != "garlic" {
		t.Errorf("ingredient = %q, want %q", body.Ingredient, "garlic")
	}
	if body.Level != "high" {
		t.Errorf("level = %q, want %q", body.Level, "high")
	}
	if body.Certainty != 0.95 {
		t.Errorf("certainty = %f, want %f", body.Certainty, 0.95)
	}
}

func TestGetFodmapHandler_NoSearcher(t *testing.T) {
	s := &Server{searcher: nil}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchFodmap/{ingredient...}", s.getFodmapHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchFodmap/garlic", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestGetFodmapHandler_EmptyIngredient(t *testing.T) {
	s := &Server{searcher: &handlersTestSearcher{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchFodmap/{ingredient...}", s.getFodmapHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchFodmap/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGetFodmapHandler_NotFound(t *testing.T) {
	mock := &handlersTestSearcher{fodmapErr: errors.New("not found")}
	s := &Server{searcher: mock}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /searchFodmap/{ingredient...}", s.getFodmapHandler)

	req := httptest.NewRequest(http.MethodGet, "/searchFodmap/unobtainium", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
