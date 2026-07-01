package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// stubRestaurantStore is a configurable in-memory stub for RestaurantStore.
type stubRestaurantStore struct {
	rows      map[string]*Restaurant
	upsertErr error
	listErr   error
	getErr    error
	updateErr error
}

func newStubRestaurantStore() *stubRestaurantStore {
	return &stubRestaurantStore{rows: make(map[string]*Restaurant)}
}

func (s *stubRestaurantStore) Upsert(_ context.Context, r Restaurant) (*Restaurant, error) {
	if s.upsertErr != nil {
		return nil, s.upsertErr
	}
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	cp := r
	key := camisKey(cp.CAMIS)
	if key != "" {
		s.rows[key] = &cp
	} else {
		s.rows[cp.ID.String()] = &cp
	}
	return &cp, nil
}

func (s *stubRestaurantStore) Get(_ context.Context, camis string) (*Restaurant, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	r, ok := s.rows[camis]
	if !ok {
		return nil, nil
	}
	return r, nil
}

func (s *stubRestaurantStore) GetByID(_ context.Context, id uuid.UUID) (*Restaurant, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if r, ok := s.rows[id.String()]; ok {
		return r, nil
	}
	for _, r := range s.rows {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, nil
}

func (s *stubRestaurantStore) List(_ context.Context, _, _ string, _, _ int) ([]Restaurant, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]Restaurant, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, *r)
	}
	return out, nil
}

func (s *stubRestaurantStore) UpdateDiscoveryURLs(_ context.Context, camis, websiteURL string, menuURLs []string, source, address, phone string) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	if r, ok := s.rows[camis]; ok {
		r.WebsiteURL = &websiteURL
		r.MenuURLs = menuURLs
		r.URLSource = &source
		r.Address = &address
		r.Phone = &phone
	}
	return nil
}

func (s *stubRestaurantStore) UpdateScrapeResult(_ context.Context, camis, status string, itemCount int, _ string) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	if r, ok := s.rows[camis]; ok {
		r.Status = status
		r.ItemCount = itemCount
	}
	return nil
}

// camisKey returns the map key for a restaurant, preferring CAMIS, falling
// back to the UUID string so lookups by either identifier work.
func camisKey(c *string) string {
	if c == nil {
		return ""
	}
	return *c
}

// strPtr is a small helper for building *string fields in test literals.
func strPtr(s string) *string { return &s }

// stubRestaurantJobQueue is a configurable stub for RestaurantJobQueue.
type stubRestaurantJobQueue struct {
	discoverErr error
	scrapeErr   error
}

func (q *stubRestaurantJobQueue) EnqueueDiscover(_ context.Context, _ Restaurant) error {
	return q.discoverErr
}

func (q *stubRestaurantJobQueue) EnqueueScrape(_ context.Context, _ Restaurant) error {
	return q.scrapeErr
}

// newRestaurantServer creates a Server with restaurant stubs wired in.
func newRestaurantServer(store *stubRestaurantStore, queue *stubRestaurantJobQueue) *Server {
	s := NewServerWithChat(nil, 0, ChatConfig{})
	s.SetRestaurantStore(store)
	if queue != nil {
		s.SetRestaurantJobQueue(queue)
	}
	return s
}

func TestRestaurantStatusNeedsRescrape(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{"failed_scrape", true},
		{"scraped", true},
		{"url_found", true},
		{"scraping", true},
		{"pending_discovery", false},
		{"discovered", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := restaurantStatusNeedsRescrape(tc.status); got != tc.want {
			t.Errorf("restaurantStatusNeedsRescrape(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestRestaurantCreateHandler(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := newStubRestaurantStore()
		s := newRestaurantServer(store, &stubRestaurantJobQueue{})

		body := `{"camis":"123","dba":"Test Diner"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		s.restaurantCreateHandler(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201", w.Code)
		}
		var got Restaurant
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if got.CAMIS == nil || *got.CAMIS != "123" || got.DBA != "Test Diner" {
			t.Errorf("response = %+v", got)
		}
		if _, ok := store.rows["123"]; !ok {
			t.Error("restaurant not saved to store")
		}
	})

	t.Run("missing camis", func(t *testing.T) {
		s := newRestaurantServer(newStubRestaurantStore(), nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants", bytes.NewBufferString(`{"dba":"Nope"}`))
		w := httptest.NewRecorder()
		s.restaurantCreateHandler(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		s := newRestaurantServer(newStubRestaurantStore(), nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants", bytes.NewBufferString(`{bad`))
		w := httptest.NewRecorder()
		s.restaurantCreateHandler(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})

	t.Run("store error", func(t *testing.T) {
		store := newStubRestaurantStore()
		store.upsertErr = errors.New("db down")
		s := newRestaurantServer(store, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants", bytes.NewBufferString(`{"camis":"x","dba":"y"}`))
		w := httptest.NewRecorder()
		s.restaurantCreateHandler(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", w.Code)
		}
	})
}

func TestRestaurantListHandler(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := newStubRestaurantStore()
		_, _ = store.Upsert(context.Background(), Restaurant{CAMIS: strPtr("1"), DBA: "A", Status: "pending_discovery"})
		s := newRestaurantServer(store, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/restaurants", nil)
		w := httptest.NewRecorder()
		s.restaurantListHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var resp map[string]any
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := resp["restaurants"]; !ok {
			t.Error("response missing 'restaurants' key")
		}
	})

	t.Run("store error", func(t *testing.T) {
		store := newStubRestaurantStore()
		store.listErr = errors.New("db down")
		s := newRestaurantServer(store, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/restaurants", nil)
		w := httptest.NewRecorder()
		s.restaurantListHandler(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", w.Code)
		}
	})
}

func TestRestaurantGetHandler(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		store := newStubRestaurantStore()
		_, _ = store.Upsert(context.Background(), Restaurant{CAMIS: strPtr("42"), DBA: "Joe's"})
		s := newRestaurantServer(store, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/restaurants/42", nil)
		req.SetPathValue("camis", "42")
		w := httptest.NewRecorder()
		s.restaurantGetHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newRestaurantServer(newStubRestaurantStore(), nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/restaurants/999", nil)
		req.SetPathValue("camis", "999")
		w := httptest.NewRecorder()
		s.restaurantGetHandler(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("store error", func(t *testing.T) {
		store := newStubRestaurantStore()
		store.getErr = errors.New("db down")
		s := newRestaurantServer(store, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/restaurants/1", nil)
		req.SetPathValue("camis", "1")
		w := httptest.NewRecorder()
		s.restaurantGetHandler(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", w.Code)
		}
	})
}

func TestRestaurantTriggerDiscoverHandler(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := newStubRestaurantStore()
		_, _ = store.Upsert(context.Background(), Restaurant{CAMIS: strPtr("1"), DBA: "A"})
		s := newRestaurantServer(store, &stubRestaurantJobQueue{})

		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants/1/discover", nil)
		req.SetPathValue("camis", "1")
		w := httptest.NewRecorder()
		s.restaurantTriggerDiscoverHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newRestaurantServer(newStubRestaurantStore(), &stubRestaurantJobQueue{})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants/missing/discover", nil)
		req.SetPathValue("camis", "missing")
		w := httptest.NewRecorder()
		s.restaurantTriggerDiscoverHandler(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("no job queue", func(t *testing.T) {
		store := newStubRestaurantStore()
		_, _ = store.Upsert(context.Background(), Restaurant{CAMIS: strPtr("1"), DBA: "A"})
		s := newRestaurantServer(store, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants/1/discover", nil)
		req.SetPathValue("camis", "1")
		w := httptest.NewRecorder()
		s.restaurantTriggerDiscoverHandler(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", w.Code)
		}
	})

	t.Run("already queued", func(t *testing.T) {
		store := newStubRestaurantStore()
		_, _ = store.Upsert(context.Background(), Restaurant{CAMIS: strPtr("1"), DBA: "A"})
		q := &stubRestaurantJobQueue{discoverErr: ErrJobAlreadyQueued}
		s := newRestaurantServer(store, q)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants/1/discover", nil)
		req.SetPathValue("camis", "1")
		w := httptest.NewRecorder()
		s.restaurantTriggerDiscoverHandler(w, req)
		if w.Code != http.StatusConflict {
			t.Errorf("status = %d, want 409", w.Code)
		}
	})
}

func TestRestaurantTriggerScrapeHandler(t *testing.T) {
	menuURLs := []string{"http://example.com/menu"}

	t.Run("success", func(t *testing.T) {
		store := newStubRestaurantStore()
		r := Restaurant{CAMIS: strPtr("1"), DBA: "A", MenuURLs: menuURLs}
		_, _ = store.Upsert(context.Background(), r)
		s := newRestaurantServer(store, &stubRestaurantJobQueue{})

		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants/1/scrape", nil)
		req.SetPathValue("camis", "1")
		w := httptest.NewRecorder()
		s.restaurantTriggerScrapeHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	})

	t.Run("no menu urls", func(t *testing.T) {
		store := newStubRestaurantStore()
		_, _ = store.Upsert(context.Background(), Restaurant{CAMIS: strPtr("1"), DBA: "A"})
		s := newRestaurantServer(store, &stubRestaurantJobQueue{})

		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants/1/scrape", nil)
		req.SetPathValue("camis", "1")
		w := httptest.NewRecorder()
		s.restaurantTriggerScrapeHandler(w, req)
		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d, want 422", w.Code)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newRestaurantServer(newStubRestaurantStore(), &stubRestaurantJobQueue{})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants/nope/scrape", nil)
		req.SetPathValue("camis", "nope")
		w := httptest.NewRecorder()
		s.restaurantTriggerScrapeHandler(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("already queued", func(t *testing.T) {
		store := newStubRestaurantStore()
		r := Restaurant{CAMIS: strPtr("1"), DBA: "A", MenuURLs: menuURLs}
		_, _ = store.Upsert(context.Background(), r)
		q := &stubRestaurantJobQueue{scrapeErr: ErrJobAlreadyQueued}
		s := newRestaurantServer(store, q)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants/1/scrape", nil)
		req.SetPathValue("camis", "1")
		w := httptest.NewRecorder()
		s.restaurantTriggerScrapeHandler(w, req)
		if w.Code != http.StatusConflict {
			t.Errorf("status = %d, want 409", w.Code)
		}
	})
}

func TestRestaurantRetryHandler(t *testing.T) {
	menuURLs := []string{"http://example.com/menu"}

	t.Run("retry scrape when has menu urls and scrape status", func(t *testing.T) {
		store := newStubRestaurantStore()
		r := Restaurant{CAMIS: strPtr("1"), DBA: "A", Status: "failed_scrape", MenuURLs: menuURLs}
		_, _ = store.Upsert(context.Background(), r)
		s := newRestaurantServer(store, &stubRestaurantJobQueue{})

		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants/1/retry", nil)
		req.SetPathValue("camis", "1")
		w := httptest.NewRecorder()
		s.restaurantRetryHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var resp map[string]string
		_ = json.NewDecoder(w.Body).Decode(&resp)
		if resp["action"] != "scrape" {
			t.Errorf("action = %q, want scrape", resp["action"])
		}
	})

	t.Run("retry discover when no menu urls", func(t *testing.T) {
		store := newStubRestaurantStore()
		r := Restaurant{CAMIS: strPtr("1"), DBA: "A", Status: "failed_scrape"}
		_, _ = store.Upsert(context.Background(), r)
		s := newRestaurantServer(store, &stubRestaurantJobQueue{})

		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants/1/retry", nil)
		req.SetPathValue("camis", "1")
		w := httptest.NewRecorder()
		s.restaurantRetryHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var resp map[string]string
		_ = json.NewDecoder(w.Body).Decode(&resp)
		if resp["action"] != "discover" {
			t.Errorf("action = %q, want discover", resp["action"])
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newRestaurantServer(newStubRestaurantStore(), &stubRestaurantJobQueue{})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants/nope/retry", nil)
		req.SetPathValue("camis", "nope")
		w := httptest.NewRecorder()
		s.restaurantRetryHandler(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("no job queue", func(t *testing.T) {
		store := newStubRestaurantStore()
		_, _ = store.Upsert(context.Background(), Restaurant{CAMIS: strPtr("1"), DBA: "A"})
		s := newRestaurantServer(store, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/restaurants/1/retry", nil)
		req.SetPathValue("camis", "1")
		w := httptest.NewRecorder()
		s.restaurantRetryHandler(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", w.Code)
		}
	})
}
