package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"fodmap/data"
	"fodmap/search"
)

func (s *Server) analyzeHandler(w http.ResponseWriter, r *http.Request) {
	businessID := r.URL.Query().Get("business_id")
	if businessID == "" {
		http.Error(w, `{"error":"business_id query parameter is required"}`, http.StatusBadRequest)
		return
	}

	job := s.store.Create(businessID)
	go s.runAnalysis(job.ID, businessID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"job_id": job.ID})
}

// runAnalysis is the background goroutine that reads reviews from the archive,
// calls the Gemini API via concurrent workers, and stores the result.
func (s *Server) runAnalysis(jobID, businessID string) {
	s.store.Update(jobID, func(j *Job) { j.Status = StatusRunning })

	reviews, err := data.GetReviewsByBusiness(businessID)
	if err != nil {
		slog.Error("fetch error", "job_id", jobID, "error", err)
		s.store.Update(jobID, func(j *Job) { j.Status = StatusFailed; j.Error = err.Error() })
		return
	}
	if len(reviews) == 0 {
		s.store.Update(jobID, func(j *Job) {
			j.Status = StatusFailed
			j.Error = "no reviews found for business_id"
		})
		return
	}

	result, err := s.llm.Analyze(context.Background(), reviews)
	if err != nil {
		slog.Error("LLM error", "job_id", jobID, "error", err)
		s.store.Update(jobID, func(j *Job) { j.Status = StatusFailed; j.Error = err.Error() })
		return
	}

	s.store.Update(jobID, func(j *Job) { j.Status = StatusComplete; j.Result = result })
	slog.Info("analysis complete", "job_id", jobID, "review_count", len(reviews))
}

func (s *Server) resultsHandler(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	job, ok := s.store.Get(jobID)
	if !ok {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}

func (s *Server) reviewsHandler(w http.ResponseWriter, r *http.Request) {
	businessID := r.URL.Query().Get("business_id")
	if businessID == "" {
		http.Error(w, `{"error":"business_id query parameter is required"}`, http.StatusBadRequest)
		return
	}

	reviews, err := data.GetReviewsByBusiness(businessID)
	if err != nil {
		slog.Error("reviewsHandler error", "error", err)
		http.Error(w, `{"error":"failed to read archive"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reviews)
}

func (s *Server) searchHandler(w http.ResponseWriter, r *http.Request) {
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
		Category: r.URL.Query().Get("category"),
		City:     r.URL.Query().Get("city"),
		State:    r.URL.Query().Get("state"),
	}

	result, err := s.searcher.Search(r.Context(), q, limit, filter)
	if err != nil {
		slog.Error("search error", "error", err)
		http.Error(w, `{"error":"search failed"}`, http.StatusInternalServerError)
		return
	}

	// Ensure JSON encodes [] not null when there are no results.
	if result.BusinessIDs == nil {
		result.BusinessIDs = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]string{"business_ids": result.BusinessIDs})
}
