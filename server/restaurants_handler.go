package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// restaurantStatusNeedsRescrape returns true if a retry should re-scrape.
func restaurantStatusNeedsRescrape(status string) bool {
	return status == "failed_scrape" || status == "scraped" || status == "url_found" || status == "scraping"
}

// restaurantsCreateRequest is the body for POST /api/v1/restaurants.
type restaurantsCreateRequest struct {
	CAMIS     string   `json:"camis"`
	DBA       string   `json:"dba"`
	Boro      *string  `json:"boro"`
	Building  *string  `json:"building"`
	Street    *string  `json:"street"`
	Zipcode   *string  `json:"zipcode"`
	Phone     *string  `json:"phone"`
	Cuisine   *string  `json:"cuisine"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
}

func (s *Server) restaurantCreateHandler(w http.ResponseWriter, r *http.Request) {
	var req restaurantsCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.CAMIS == "" || req.DBA == "" {
		respondError(w, "camis and dba are required", http.StatusBadRequest)
		return
	}

	rest := Restaurant{
		CAMIS:     &req.CAMIS,
		DBA:       req.DBA,
		Boro:      req.Boro,
		Building:  req.Building,
		Street:    req.Street,
		Zipcode:   req.Zipcode,
		Phone:     req.Phone,
		Cuisine:   req.Cuisine,
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
		Status:    "pending_discovery",
	}

	ctx := r.Context()
	upserted, err := s.restaurantStore.Upsert(ctx, rest)
	if err != nil {
		slog.Error("restaurants: upsert", "camis", req.CAMIS, "err", err)
		if strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "duplicate key value") {
			respondError(w, "restaurant already exists", http.StatusConflict)
			return
		}
		respondError(w, "failed to save restaurant", http.StatusInternalServerError)
		return
	}

	if s.restaurantJobQueue != nil {
		if err := s.restaurantJobQueue.EnqueueDiscover(ctx, *upserted); err != nil {
			slog.Error("restaurants: enqueue discover", "camis", req.CAMIS, "err", err)
			// Non-fatal: row was saved; caller can trigger manually.
		}
	}

	got, err := s.restaurantStore.Get(ctx, req.CAMIS)
	if err != nil || got == nil {
		// Row saved but can't re-read; return minimal response.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(rest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(got)
}

func (s *Server) restaurantListHandler(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	search := r.URL.Query().Get("search")

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > 200 {
		limit = 200
	}

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	ctx := r.Context()
	rows, err := s.restaurantStore.List(ctx, status, search, limit, offset)
	if err != nil {
		slog.Error("restaurants: list", "err", err)
		respondError(w, "failed to list restaurants", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"restaurants": rows,
		"limit":       limit,
		"offset":      offset,
	})
}

func (s *Server) restaurantGetHandler(w http.ResponseWriter, r *http.Request) {
	camis := r.PathValue("camis")
	ctx := r.Context()

	row, err := s.restaurantStore.Get(ctx, camis)
	if err != nil {
		slog.Error("restaurants: get", "camis", camis, "err", err)
		respondError(w, "failed to get restaurant", http.StatusInternalServerError)
		return
	}
	if row == nil {
		respondError(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(row)
}

func (s *Server) restaurantTriggerDiscoverHandler(w http.ResponseWriter, r *http.Request) {
	camis := r.PathValue("camis")
	ctx := r.Context()

	row, err := s.restaurantStore.Get(ctx, camis)
	if err != nil {
		slog.Error("restaurants: get for discover trigger", "camis", camis, "err", err)
		respondError(w, "failed to get restaurant", http.StatusInternalServerError)
		return
	}
	if row == nil {
		respondError(w, "not found", http.StatusNotFound)
		return
	}

	if s.restaurantJobQueue == nil {
		respondError(w, "job queue not configured", http.StatusServiceUnavailable)
		return
	}
	if err := s.restaurantJobQueue.EnqueueDiscover(ctx, *row); err != nil {
		if errors.Is(err, ErrJobAlreadyQueued) {
			respondError(w, "discovery job already queued", http.StatusConflict)
			return
		}
		slog.Error("restaurants: enqueue discover", "camis", camis, "err", err)
		respondError(w, "failed to enqueue discovery", http.StatusInternalServerError)
		return
	}
	if err := s.restaurantStore.UpdateScrapeResult(ctx, camis, "pending_discovery", 0, ""); err != nil {
		slog.Warn("restaurants: update status after enqueue discover", "camis", camis, "err", err)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
}

func (s *Server) restaurantTriggerScrapeHandler(w http.ResponseWriter, r *http.Request) {
	camis := r.PathValue("camis")
	ctx := r.Context()

	row, err := s.restaurantStore.Get(ctx, camis)
	if err != nil {
		slog.Error("restaurants: get for scrape trigger", "camis", camis, "err", err)
		respondError(w, "failed to get restaurant", http.StatusInternalServerError)
		return
	}
	if row == nil {
		respondError(w, "not found", http.StatusNotFound)
		return
	}
	if len(row.MenuURLs) == 0 {
		respondError(w, "restaurant has no menu_urls; run discover first", http.StatusUnprocessableEntity)
		return
	}

	if s.restaurantJobQueue == nil {
		respondError(w, "job queue not configured", http.StatusServiceUnavailable)
		return
	}
	if err := s.restaurantJobQueue.EnqueueScrape(ctx, *row); err != nil {
		if errors.Is(err, ErrJobAlreadyQueued) {
			respondError(w, "scrape job already queued", http.StatusConflict)
			return
		}
		slog.Error("restaurants: enqueue scrape", "camis", camis, "err", err)
		respondError(w, "failed to enqueue scrape", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
}

func (s *Server) restaurantRetryHandler(w http.ResponseWriter, r *http.Request) {
	camis := r.PathValue("camis")
	ctx := r.Context()

	row, err := s.restaurantStore.Get(ctx, camis)
	if err != nil {
		slog.Error("restaurants: get for retry", "camis", camis, "err", err)
		respondError(w, "failed to get restaurant", http.StatusInternalServerError)
		return
	}
	if row == nil {
		respondError(w, "not found", http.StatusNotFound)
		return
	}

	if s.restaurantJobQueue == nil {
		respondError(w, "job queue not configured", http.StatusServiceUnavailable)
		return
	}

	var action string
	if restaurantStatusNeedsRescrape(row.Status) && len(row.MenuURLs) > 0 {
		if err := s.restaurantJobQueue.EnqueueScrape(ctx, *row); err != nil && !errors.Is(err, ErrJobAlreadyQueued) {
			slog.Error("restaurants: retry enqueue scrape", "camis", camis, "err", err)
			respondError(w, "failed to enqueue scrape", http.StatusInternalServerError)
			return
		}
		if err := s.restaurantStore.UpdateScrapeResult(ctx, camis, "url_found", 0, ""); err != nil {
			slog.Warn("restaurants: update status after retry enqueue scrape", "camis", camis, "err", err)
		}
		action = "scrape"
	} else {
		if err := s.restaurantJobQueue.EnqueueDiscover(ctx, *row); err != nil && !errors.Is(err, ErrJobAlreadyQueued) {
			slog.Error("restaurants: retry enqueue discover", "camis", camis, "err", err)
			respondError(w, "failed to enqueue discovery", http.StatusInternalServerError)
			return
		}
		if err := s.restaurantStore.UpdateScrapeResult(ctx, camis, "pending_discovery", 0, ""); err != nil {
			slog.Warn("restaurants: update status after retry enqueue discover", "camis", camis, "err", err)
		}
		action = "discover"
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "queued", "action": action})
}
