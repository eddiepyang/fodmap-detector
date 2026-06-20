package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"fodmap/data"
	"fodmap/fodmap/store"
)

// adminIngredientStatsHandler returns aggregate counts by level and group.
func (s *Server) adminIngredientStatsHandler(w http.ResponseWriter, r *http.Request) {
	stats, err := s.catalogStore.Stats(r.Context())
	if err != nil {
		slog.Error("failed to get ingredient stats", "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"total_count":  stats.TotalCount,
		"level_counts": stats.LevelCounts,
		"group_counts": stats.GroupCounts,
	})
}

// adminIngredientSearchTestHandler runs a semantic search and returns the
// matched ingredient plus certainty.
func (s *Server) adminIngredientSearchTestHandler(w http.ResponseWriter, r *http.Request) {
	if s.searcher == nil {
		respondError(w, "search service not configured", http.StatusServiceUnavailable)
		return
	}

	q := r.URL.Query().Get("q")
	if q == "" {
		respondError(w, "q query parameter is required", http.StatusBadRequest)
		return
	}

	res, cert, err := s.searcher.SearchFodmap(r.Context(), q)
	if err != nil {
		respondError(w, "search failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"match": map[string]any{
			"ingredient":    res.Ingredient,
			"level":         res.Level,
			"groups":        res.Groups,
			"notes":         res.Notes,
			"substitutions": res.Substitutions,
		},
		"certainty": cert,
	})
}

// adminListIngredientsHandler lists ingredients with optional filters and pagination.
func (s *Server) adminListIngredientsHandler(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	level := strings.TrimSpace(r.URL.Query().Get("level"))
	group := strings.TrimSpace(r.URL.Query().Get("group"))

	page, limit := parsePageLimit(r.URL.Query().Get("page"), r.URL.Query().Get("limit"))
	offset := (page - 1) * limit

	filter := store.ListFilter{Search: search, Level: level, Group: group}
	items, err := s.catalogStore.List(r.Context(), offset, limit, filter)
	if err != nil {
		slog.Error("failed to list ingredients", "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	total, err := s.catalogStore.Count(r.Context(), filter)
	if err != nil {
		slog.Error("failed to count ingredients", "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, ingredientResponse(item))
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ingredients": out,
		"total":       total,
		"page":        page,
		"limit":       limit,
	})
}

// adminGetIngredientHandler returns a single ingredient by name.
func (s *Server) adminGetIngredientHandler(w http.ResponseWriter, r *http.Request) {
	name, err := ingredientPathName(r)
	if err != nil {
		respondError(w, err.Error(), http.StatusBadRequest)
		return
	}

	item, err := s.catalogStore.Ingredient(r.Context(), name)
	if err != nil {
		slog.Error("failed to get ingredient", "name", name, "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if item == nil {
		respondError(w, "ingredient not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ingredientResponse(*item))
}

// adminCreateIngredientHandler creates a new ingredient. It rejects duplicates
// with 409 and returns 201 on success.
func (s *Server) adminCreateIngredientHandler(w http.ResponseWriter, r *http.Request) {
	var req ingredientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	normalizedName, err := validateIngredientRequest(req)
	if err != nil {
		respondError(w, err.Error(), http.StatusBadRequest)
		return
	}

	dedupedGroups := dedupe(req.Groups)
	dedupedSubs := dedupe(req.Substitutions)
	canonicalName := strings.ToLower(normalizedName)

	entry := store.CatalogEntry{
		Ingredient:    canonicalName,
		Level:         req.Level,
		Groups:        dedupedGroups,
		Notes:         req.Notes,
		Substitutions: dedupedSubs,
	}

	if err := s.catalogStore.Create(r.Context(), entry); err != nil {
		if errors.Is(err, store.ErrIngredientExists) {
			respondError(w, "ingredient already exists", http.StatusConflict)
			return
		}
		slog.Error("failed to create ingredient", "name", canonicalName, "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	warning := s.syncToSearcher(r.Context(), canonicalName, data.FodmapEntry{
		Level:         req.Level,
		Groups:        dedupedGroups,
		Notes:         req.Notes,
		Substitutions: dedupedSubs,
	})

	resp := ingredientResponse(entry)
	if warning != "" {
		resp["warning"] = warning
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// adminUpdateIngredientHandler performs a strict update of an existing
// ingredient. The ingredient name is immutable in v1.
func (s *Server) adminUpdateIngredientHandler(w http.ResponseWriter, r *http.Request) {
	name, err := ingredientPathName(r)
	if err != nil {
		respondError(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req ingredientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	normalizedName, err := validateIngredientRequest(req)
	if err != nil {
		respondError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if !strings.EqualFold(normalizedName, name) {
		respondError(w, "ingredient name cannot be changed", http.StatusBadRequest)
		return
	}

	dedupedGroups := dedupe(req.Groups)
	dedupedSubs := dedupe(req.Substitutions)

	entry := store.CatalogEntry{
		Ingredient:    normalizedName,
		Level:         req.Level,
		Groups:        dedupedGroups,
		Notes:         req.Notes,
		Substitutions: dedupedSubs,
	}

	if err := s.catalogStore.Update(r.Context(), name, entry); err != nil {
		if errors.Is(err, store.ErrIngredientNotFound) {
			respondError(w, "ingredient not found", http.StatusNotFound)
			return
		}
		slog.Error("failed to update ingredient", "name", name, "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	warning := s.syncToSearcher(r.Context(), normalizedName, data.FodmapEntry{
		Level:         req.Level,
		Groups:        dedupedGroups,
		Notes:         req.Notes,
		Substitutions: dedupedSubs,
	})

	resp := ingredientResponse(entry)
	if warning != "" {
		resp["warning"] = warning
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// adminDeleteIngredientHandler deletes an ingredient from the catalog and best-effort syncs to search.
func (s *Server) adminDeleteIngredientHandler(w http.ResponseWriter, r *http.Request) {
	name, err := ingredientPathName(r)
	if err != nil {
		respondError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.catalogStore.Delete(r.Context(), name); err != nil {
		slog.Error("failed to delete ingredient", "name", name, "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	warning := ""
	if fw, ok := s.searcher.(FodmapWriter); ok && s.searcher != nil {
		if err := fw.DeleteFodmapItem(r.Context(), name); err != nil {
			slog.Error("failed to sync ingredient delete to search index", "name", name, "error", err)
			warning = "search index sync pending"
		}
	}

	resp := map[string]any{"message": "ingredient deleted"}
	if warning != "" {
		resp["warning"] = warning
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// adminReseedIngredientsHandler re-upserts the static FodmapDB map into the
// catalog (overwriting existing entries with the defaults) and then rebuilds
// the vector search index from the full catalog.
func (s *Server) adminReseedIngredientsHandler(w http.ResponseWriter, r *http.Request) {
	if s.catalogStore == nil {
		respondError(w, "catalog store not configured", http.StatusServiceUnavailable)
		return
	}

	count, err := s.catalogStore.Reseed(r.Context(), data.FodmapDB)
	if err != nil {
		slog.Error("failed to reseed ingredients", "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	slog.Info("reseeded fodmap catalog", "count", count)

	warning := ""
	if s.searcher != nil {
		items, err := s.catalogStore.ListAll(r.Context())
		if err != nil {
			slog.Error("failed to list catalog for index rebuild after reseed", "error", err)
			warning = "search index sync pending"
		} else if err := s.searcher.BatchUpsertFodmap(r.Context(), store.ToMap(items)); err != nil {
			slog.Error("failed to rebuild search index after reseed", "error", err)
			warning = "search index sync pending"
		} else {
			slog.Info("rebuilt search index after reseed", "count", len(items))
		}
	}

	resp := map[string]any{
		"reseeded": true,
		"count":    count,
	}
	if warning != "" {
		resp["warning"] = warning
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ingredientRequest is the JSON body for create/update requests.
type ingredientRequest struct {
	Name          string   `json:"name"`
	Level         string   `json:"level"`
	Groups        []string `json:"groups"`
	Notes         string   `json:"notes"`
	Substitutions []string `json:"substitutions"`
}

// validateIngredientRequest validates and normalizes the request. It returns
// the normalized name or a descriptive error.
func validateIngredientRequest(req ingredientRequest) (string, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return "", errors.New("name is required")
	}
	if strings.ContainsFunc(name, unicode.IsControl) {
		return "", errors.New("name contains invalid characters")
	}
	if len(name) > 200 {
		return "", errors.New("name must be at most 200 characters")
	}
	if req.Level == "" {
		return "", errors.New("level is required")
	}
	if !contains(data.ValidFodmapLevels, req.Level) {
		return "", errors.New("invalid level")
	}
	if hasDuplicates(req.Groups) {
		return "", errors.New("duplicate groups")
	}
	for _, g := range req.Groups {
		if !contains(data.ValidFodmapGroups, g) {
			return "", errors.New("invalid group: " + g)
		}
	}
	return name, nil
}

// ingredientPathName extracts and normalizes the ingredient name from the URL path.
func ingredientPathName(r *http.Request) (string, error) {
	name := r.PathValue("name")
	if name == "" {
		return "", errors.New("missing ingredient name")
	}
	decoded, err := url.QueryUnescape(name)
	if err != nil {
		return "", errors.New("invalid ingredient name encoding")
	}
	return strings.ToLower(strings.TrimSpace(decoded)), nil
}

// ingredientResponse builds the JSON representation of a catalog entry.
func ingredientResponse(item store.CatalogEntry) map[string]any {
	return map[string]any{
		"ingredient":    item.Ingredient,
		"level":         item.Level,
		"groups":        item.Groups,
		"notes":         item.Notes,
		"substitutions": item.Substitutions,
		"updated_at":    item.UpdatedAt,
	}
}

// syncToSearcher best-effort upserts a single ingredient to the vector index.
// It returns a warning string if sync fails, empty string on success or no searcher.
func (s *Server) syncToSearcher(ctx context.Context, name string, entry data.FodmapEntry) string {
	if s.searcher == nil {
		return ""
	}
	fw, ok := s.searcher.(FodmapWriter)
	if !ok {
		return "search index sync pending"
	}
	if err := fw.UpsertFodmapItem(ctx, name, entry); err != nil {
		slog.Error("failed to sync ingredient to search index", "name", name, "error", err)
		return "search index sync pending"
	}
	return ""
}

func parsePageLimit(pageStr, limitStr string) (page, limit int) {
	page = 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p >= 1 {
			page = p
		}
	}

	limit = 20
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l >= 1 {
			limit = l
		}
	}
	if limit > 100 {
		limit = 100
	}
	return page, limit
}

func dedupe(slice []string) []string {
	seen := make(map[string]struct{}, len(slice))
	out := make([]string, 0, len(slice))
	for _, s := range slice {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func hasDuplicates(slice []string) bool {
	seen := make(map[string]struct{}, len(slice))
	for _, s := range slice {
		if _, ok := seen[s]; ok {
			return true
		}
		seen[s] = struct{}{}
	}
	return false
}
