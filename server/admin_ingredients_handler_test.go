package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"fodmap/auth"
	"fodmap/data"
	"fodmap/fodmap/store"
	"fodmap/search"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubFodmapWriter tracks upsert/delete calls for assertion.
type stubFodmapWriter struct {
	*MockSearcher
	upserts   map[string]data.FodmapEntry
	deletes   []string
	upsertErr error
	deleteErr error
}

// Ensure stubFodmapWriter satisfies server.FodmapWriter.
var _ FodmapWriter = (*stubFodmapWriter)(nil)

func (s *stubFodmapWriter) UpsertFodmapItem(ctx context.Context, name string, entry data.FodmapEntry) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	if s.upserts == nil {
		s.upserts = make(map[string]data.FodmapEntry)
	}
	s.upserts[name] = entry
	return nil
}

func (s *stubFodmapWriter) DeleteFodmapItem(ctx context.Context, name string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deletes = append(s.deletes, name)
	return nil
}

func (s *stubFodmapWriter) SearchFodmap(ctx context.Context, ingredient string) (search.FodmapResult, float64, error) {
	return search.FodmapResult{
		Ingredient:    "garlic",
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "",
		Substitutions: []string{},
	}, 0.95, nil
}

type noOpSearcher struct{}

var _ Searcher = (*noOpSearcher)(nil)

func (n *noOpSearcher) GetBusinesses(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchResult, error) {
	return search.SearchResult{}, nil
}
func (n *noOpSearcher) GetReviews(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchReviews, error) {
	return search.SearchReviews{}, nil
}
func (n *noOpSearcher) SearchFodmap(ctx context.Context, ingredient string) (search.FodmapResult, float64, error) {
	return search.FodmapResult{}, 0, errors.New("not found")
}
func (n *noOpSearcher) EnsureSchema(ctx context.Context) error                 { return nil }
func (n *noOpSearcher) EnsureFodmapSchema(ctx context.Context) error          { return nil }
func (n *noOpSearcher) BatchUpsertFodmap(ctx context.Context, items map[string]data.FodmapEntry) error {
	return nil
}
func (n *noOpSearcher) BatchUpsert(ctx context.Context, items []search.IndexItem) error { return nil }

func adminIngredientTestServer(t *testing.T) (*Server, *stubCatalogStore, string) {
	t.Helper()
	secret := "test-secret"
	adminID := "admin-1"
	us := newMockStore()
	us.users["admin@example.com"] = &auth.User{
		ID:     adminID,
		Email:  "admin@example.com",
		Role:   "admin",
		Status: "active",
	}
	cs := newStubCatalogStore()
	s := &Server{
		userStore:    us,
		catalogStore: cs,
		jwtSecret:    secret,
	}
	adminToken, _, _ := auth.GenerateTokensWithRole(adminID, "admin", secret)
	return s, cs, adminToken
}

// stubCatalogStore wraps the in-memory store and exposes helper methods for tests.
type stubCatalogStore struct {
	*inMemoryCatalogStore
}

func newStubCatalogStore() *stubCatalogStore {
	return &stubCatalogStore{inMemoryCatalogStore: newInMemoryCatalogStore().(*inMemoryCatalogStore)}
}

func TestAdminIngredientHandlers_Stats(t *testing.T) {
	s, cs, token := adminIngredientTestServer(t)
	_ = cs.Create(context.Background(), store.CatalogEntry{
		Ingredient: "garlic",
		Level:      "high",
		Groups:     []string{"fructans"},
	})
	_ = cs.Create(context.Background(), store.CatalogEntry{
		Ingredient: "rice",
		Level:      "low",
	})

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ingredients/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		TotalCount  int            `json:"total_count"`
		LevelCounts map[string]int `json:"level_counts"`
		GroupCounts map[string]int `json:"group_counts"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, 2, resp.TotalCount)
	assert.Equal(t, 1, resp.LevelCounts["high"])
	assert.Equal(t, 1, resp.GroupCounts["fructans"])
}

func TestAdminIngredientHandlers_ListAndFilter(t *testing.T) {
	s, cs, token := adminIngredientTestServer(t)
	_ = cs.Create(context.Background(), store.CatalogEntry{Ingredient: "garlic", Level: "high", Groups: []string{"fructans"}})
	_ = cs.Create(context.Background(), store.CatalogEntry{Ingredient: "onion", Level: "high", Groups: []string{"fructans"}})
	_ = cs.Create(context.Background(), store.CatalogEntry{Ingredient: "rice", Level: "low"})

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ingredients?level=high", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, float64(2), resp["total"])
	assert.Len(t, resp["ingredients"].([]any), 2)
}

func TestAdminIngredientHandlers_Get(t *testing.T) {
	s, cs, token := adminIngredientTestServer(t)
	_ = cs.Create(context.Background(), store.CatalogEntry{Ingredient: "garlic", Level: "high"})

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ingredients/garlic", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "garlic", resp["ingredient"])
}

func TestAdminIngredientHandlers_GetNotFound(t *testing.T) {
	s, _, token := adminIngredientTestServer(t)
	mux := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ingredients/missing", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminIngredientHandlers_Create(t *testing.T) {
	fw := &stubFodmapWriter{}
	s, _, token := adminIngredientTestServer(t)
	s.searcher = fw

	body, _ := json.Marshal(map[string]any{
		"name":          "Garlic",
		"level":         "high",
		"groups":        []string{"fructans"},
		"notes":         "",
		"substitutions": []string{"garlic oil"},
	})

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ingredients", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	entry, ok := fw.upserts["garlic"]
	require.True(t, ok)
	assert.Equal(t, "high", entry.Level)
	assert.Equal(t, []string{"fructans"}, entry.Groups)
}

func TestAdminIngredientHandlers_CreateDuplicate(t *testing.T) {
	s, cs, token := adminIngredientTestServer(t)
	_ = cs.Create(context.Background(), store.CatalogEntry{Ingredient: "garlic", Level: "high"})

	body, _ := json.Marshal(map[string]any{
		"name":  "Garlic",
		"level": "high",
	})

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ingredients", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestAdminIngredientHandlers_CreateValidation(t *testing.T) {
	s, _, token := adminIngredientTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"name":  "  ",
		"level": "high",
	})

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ingredients", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminIngredientHandlers_Update(t *testing.T) {
	fw := &stubFodmapWriter{}
	s, cs, token := adminIngredientTestServer(t)
	s.searcher = fw
	_ = cs.Create(context.Background(), store.CatalogEntry{Ingredient: "garlic", Level: "high", Groups: []string{"fructans"}})

	body, _ := json.Marshal(map[string]any{
		"name":   "garlic",
		"level":  "low",
		"groups": []string{},
	})

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/ingredients/garlic", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	item, err := cs.Get(context.Background(), "garlic")
	require.NoError(t, err)
	assert.Equal(t, "low", item.Level)
	entry, ok := fw.upserts["garlic"]
	require.True(t, ok)
	assert.Equal(t, "low", entry.Level)
}

func TestAdminIngredientHandlers_UpdateNotFound(t *testing.T) {
	s, _, token := adminIngredientTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"name":   "missing",
		"level":  "low",
		"groups": []string{},
	})

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/ingredients/missing", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminIngredientHandlers_UpdateImmutableName(t *testing.T) {
	s, cs, token := adminIngredientTestServer(t)
	_ = cs.Create(context.Background(), store.CatalogEntry{Ingredient: "garlic", Level: "high"})

	body, _ := json.Marshal(map[string]any{
		"name":   "onion",
		"level":  "high",
		"groups": []string{},
	})

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/ingredients/garlic", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminIngredientHandlers_Delete(t *testing.T) {
	fw := &stubFodmapWriter{}
	s, cs, token := adminIngredientTestServer(t)
	s.searcher = fw
	_ = cs.Create(context.Background(), store.CatalogEntry{Ingredient: "garlic", Level: "high"})

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/ingredients/garlic", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	item, err := cs.Get(context.Background(), "garlic")
	require.NoError(t, err)
	assert.Nil(t, item)
	assert.Contains(t, fw.deletes, "garlic")
}

func TestAdminIngredientHandlers_SearchTest(t *testing.T) {
	fw := &stubFodmapWriter{}
	s, _, token := adminIngredientTestServer(t)
	s.searcher = fw

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ingredients/search-test?q=garlic+powder", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	match := resp["match"].(map[string]any)
	assert.Equal(t, "garlic", match["ingredient"])
	assert.Equal(t, 0.95, resp["certainty"])
}

func TestAdminIngredientHandlers_SearchTestNoSearcher(t *testing.T) {
	s, _, token := adminIngredientTestServer(t)

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ingredients/search-test?q=garlic", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestAdminIngredientHandlers_URLEncodedName(t *testing.T) {
	s, cs, token := adminIngredientTestServer(t)
	_ = cs.Create(context.Background(), store.CatalogEntry{Ingredient: "salt & pepper", Level: "low"})

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ingredients/"+url.QueryEscape("salt & pepper"), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "salt & pepper", resp["ingredient"])
}

func TestAdminIngredientHandlers_MuxPrecedence(t *testing.T) {
	s, _, token := adminIngredientTestServer(t)

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ingredients/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	_, ok := resp["total_count"]
	assert.True(t, ok, "stats endpoint should return total_count")
}

func TestAdminIngredientHandlers_SyncFailureReturnsWarning(t *testing.T) {
	fw := &stubFodmapWriter{upsertErr: errors.New("search down")}
	s, _, token := adminIngredientTestServer(t)
	s.searcher = fw

	body, _ := json.Marshal(map[string]any{
		"name":   "garlic",
		"level":  "high",
		"groups": []string{"fructans"},
	})

	mux := s.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ingredients", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "search index sync pending", resp["warning"])
}

func TestAdminIngredientHandlers_Unauthorized(t *testing.T) {
	s, _, _ := adminIngredientTestServer(t)
	mux := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ingredients", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
