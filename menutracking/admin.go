package menutracking

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"fodmap/menutracking/store"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DiscardedJob represents a river job in the discarded state.
type DiscardedJob struct {
	Kind           string          `json:"kind"`
	Args           json.RawMessage `json:"args"`
	FinalAttemptAt string          `json:"final_attempt_at,omitempty"`
	State          string          `json:"state"`
	CreatedAt      string          `json:"created_at"`
}

// AdminHandler returns an HTTP handler that serves menutracking admin
// endpoints. It requires a pgxpool.Pool to query river and domain tables.
type AdminHandler struct {
	Pool         *pgxpool.Pool
	ReloadSignal chan struct{} // written to on POST /menutracking/reload to trigger periodic job refresh
}

// ServeHTTP routes menutracking admin requests based on the path.
func (h *AdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/menutracking/sources", "/menutracking/sources/":
		h.listSources(w, r)
	case "/menutracking/jobs", "/menutracking/jobs/":
		h.listDiscardedJobs(w, r)
	case "/menutracking/reload", "/menutracking/reload/":
		h.reloadSources(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *AdminHandler) listSources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	sources, err := ListSources(ctx, h.Pool)
	if err != nil {
		slog.Error("menutracking admin: listing sources", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(sources); err != nil {
		slog.Error("menutracking admin: encoding sources", "err", err)
	}
}

func (h *AdminHandler) listDiscardedJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	rows, err := h.Pool.Query(ctx, store.ListDiscardedJobsSQL, limit)
	if err != nil {
		slog.Error("menutracking admin: listing discarded jobs", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var jobs []DiscardedJob
	for rows.Next() {
		var j DiscardedJob
		if err := rows.Scan(&j.Kind, &j.Args, &j.FinalAttemptAt, &j.State, &j.CreatedAt); err != nil {
			slog.Error("menutracking admin: scanning discarded job", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		jobs = append(jobs, j)
	}
	if jobs == nil {
		jobs = []DiscardedJob{}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(jobs); err != nil {
		slog.Error("menutracking admin: encoding discarded jobs", "err", err)
	}
}

func (h *AdminHandler) reloadSources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.ReloadSignal != nil {
		select {
		case h.ReloadSignal <- struct{}{}:
		default:
			// Signal already pending, don't block.
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "reload_signaled"}); err != nil {
		slog.Error("menutracking admin: encoding reload response", "err", err)
	}
}
