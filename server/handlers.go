package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"fodmap/data"
	"fodmap/search"
)



func (s *Server) reviewsHandler(w http.ResponseWriter, r *http.Request) {
	businessID := r.URL.Query().Get("business_id")
	if businessID == "" {
		http.Error(w, `{"error":"business_id query parameter is required"}`, http.StatusBadRequest)
		return
	}

	reviews, err := data.GetReviewsByBusiness("", businessID)
	if err != nil {
		slog.Error("reviewsHandler error", "error", err)
		http.Error(w, `{"error":"failed to read archive"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(reviews); err != nil {
		slog.Error("encode error", "error", err)
	}
}

func (s *Server) getBusinessesHandler(w http.ResponseWriter, r *http.Request) {
	if s.searcher == nil {
		http.Error(w, `{"error":"search service not configured"}`, http.StatusServiceUnavailable)
		return
	}

	q := r.PathValue("query")
	if q == "" {
		http.Error(w, `{"error":"search query is required"}`, http.StatusBadRequest)
		return
	}

	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n <= 0 {
			http.Error(w, `{"error":"limit must be a positive integer"}`, http.StatusBadRequest)
			return
		}
		limit = n
	}

	filter := search.SearchFilter{
		Category: strings.TrimSpace(r.URL.Query().Get("category")),
		City:     strings.TrimSpace(r.URL.Query().Get("city")),
		State:    strings.TrimSpace(r.URL.Query().Get("state")),
	}

	result, err := s.searcher.GetBusinesses(r.Context(), q, limit, filter)
	if err != nil {
		slog.Error("search error", "error", err)
		http.Error(w, `{"error":"search failed"}`, http.StatusInternalServerError)
		return
	}

	// Ensure JSON encodes [] not null when there are no results.
	if result.Businesses == nil {
		result.Businesses = []search.BusinessResult{}
	}

	type business struct {
		ID     string  `json:"id"`
		Name   string  `json:"name"`
		City   string  `json:"city"`
		State  string  `json:"state"`
		Rating float64 `json:"rating"`
		Score  float64 `json:"score"`
	}
	out := make([]business, len(result.Businesses))
	for i, b := range result.Businesses {
		out[i] = business{ID: b.ID, Name: b.Name, City: b.City, State: b.State, Rating: b.Stars, Score: b.Score}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string][]business{"businesses": out}); err != nil {
		slog.Error("encode error", "error", err)
	}
}

func (s *Server) getReviewsHandler(w http.ResponseWriter, r *http.Request) {
	if s.searcher == nil {
		http.Error(w, `{"error":"search service not configured"}`, http.StatusServiceUnavailable)
		return
	}

	q := r.PathValue("query")
	if q == "" {
		http.Error(w, `{"error":"search query is required"}`, http.StatusBadRequest)
		return
	}

	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n <= 0 {
			http.Error(w, `{"error":"limit must be a positive integer"}`, http.StatusBadRequest)
			return
		}
		limit = n
	}

	filter := search.SearchFilter{
		Category:   r.URL.Query().Get("category"),
		City:       r.URL.Query().Get("city"),
		State:      r.URL.Query().Get("state"),
		BusinessID: r.URL.Query().Get("business_id"),
	}

	result, err := s.searcher.GetReviews(r.Context(), q, limit, filter)
	if err != nil {
		slog.Error("search error", "error", err)
		http.Error(w, `{"error":"search failed"}`, http.StatusInternalServerError)
		return
	}

	type review struct {
		Text         string  `json:"text"`
		BusinessID   string  `json:"business_id"`
		BusinessName string  `json:"business_name"`
		City         string  `json:"city"`
		State        string  `json:"state"`
		Score        float64 `json:"score"`
	}

	// Ensure JSON encodes [] not null when there are no results.
	if result.BusinessReviews == nil {
		result.BusinessReviews = []search.RankedReview{}
	}

	out := make([]review, len(result.BusinessReviews))
	for i, rr := range result.BusinessReviews {
		out[i] = review{
			Text:         rr.Review.Review.Text,
			BusinessID:   rr.Review.Review.BusinessID,
			BusinessName: rr.Review.BusinessName,
			City:         rr.Review.City,
			State:        rr.Review.State,
			Score:        rr.Score,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string][]review{"reviews": out}); err != nil {
		slog.Error("encode error", "error", err)
	}
}

func (s *Server) getFodmapHandler(w http.ResponseWriter, r *http.Request) {
	if s.searcher == nil {
		http.Error(w, `{"error":"search service not configured"}`, http.StatusServiceUnavailable)
		return
	}

	ingredient := r.PathValue("ingredient")
	if ingredient == "" {
		http.Error(w, `{"error":"ingredient is required"}`, http.StatusBadRequest)
		return
	}

	res, cert, err := s.searcher.SearchFodmap(r.Context(), ingredient)
	if err != nil {
		slog.Error("search fodmap error", "error", err)
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	type response struct {
		Ingredient string   `json:"ingredient"`
		Level      string   `json:"level"`
		Groups     []string `json:"groups"`
		Notes      string   `json:"notes"`
		Certainty  float64  `json:"certainty"`
	}
	out := response{
		Ingredient: res.Ingredient,
		Level:      res.Level,
		Groups:     res.Groups,
		Notes:      res.Notes,
		Certainty:  cert,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		slog.Error("encode error", "error", err)
	}
}
