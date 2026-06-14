package server

import (
	"context"
	"strings"
	"sync"
	"time"

	"fodmap/data"
	"fodmap/fodmap/store"
)

// inMemoryCatalogStore is a thread-safe in-memory implementation of
// CatalogStore for tests.
type inMemoryCatalogStore struct {
	mu     sync.RWMutex
	items  map[string]store.CatalogEntry
	seeded bool
}

func newInMemoryCatalogStore() CatalogStore {
	return &inMemoryCatalogStore{
		items: make(map[string]store.CatalogEntry),
	}
}

// EnsureSchema is a no-op for the in-memory store.
func (s *inMemoryCatalogStore) EnsureSchema(ctx context.Context) error { return nil }

// Close is a no-op for the in-memory store.
func (s *inMemoryCatalogStore) Close() error { return nil }

// Create inserts a new ingredient into the in-memory store.
func (s *inMemoryCatalogStore) Create(ctx context.Context, entry store.CatalogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := strings.ToLower(strings.TrimSpace(entry.Ingredient))
	if _, exists := s.items[name]; exists {
		return store.ErrIngredientExists
	}
	entry.Ingredient = name
	s.items[name] = entry
	return nil
}

// Get retrieves a single ingredient by name.
func (s *inMemoryCatalogStore) Get(ctx context.Context, name string) (*store.CatalogEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.items[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

// List returns a paginated list of ingredients matching the filter.
func (s *inMemoryCatalogStore) List(ctx context.Context, offset, limit int, filter store.ListFilter) ([]store.CatalogEntry, error) {
	s.mu.RLock()
	all := s.filteredItems(filter)
	s.mu.RUnlock()
	if offset > len(all) {
		return []store.CatalogEntry{}, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end], nil
}

// Count returns the total number of ingredients matching the filter.
func (s *inMemoryCatalogStore) Count(ctx context.Context, filter store.ListFilter) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.filteredItems(filter)), nil
}

// Stats returns aggregate counts by level and group.
func (s *inMemoryCatalogStore) Stats(ctx context.Context) (*store.Stats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := &store.Stats{
		TotalCount:  len(s.items),
		LevelCounts: make(map[string]int),
		GroupCounts: make(map[string]int),
	}
	for _, e := range s.items {
		st.LevelCounts[e.Level]++
		for _, g := range e.Groups {
			st.GroupCounts[g]++
		}
	}
	return st, nil
}

// Update performs a strict update of an existing ingredient.
func (s *inMemoryCatalogStore) Update(ctx context.Context, name string, entry store.CatalogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(strings.TrimSpace(name))
	if _, ok := s.items[key]; !ok {
		return store.ErrIngredientNotFound
	}
	entry.Ingredient = key
	s.items[key] = entry
	return nil
}

// Delete removes an ingredient from the in-memory store.
func (s *inMemoryCatalogStore) Delete(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, strings.ToLower(strings.TrimSpace(name)))
	return nil
}

// ListAll returns every ingredient in the in-memory store.
func (s *inMemoryCatalogStore) ListAll(ctx context.Context) ([]store.CatalogEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]store.CatalogEntry, 0, len(s.items))
	for _, e := range s.items {
		out = append(out, e)
	}
	return out, nil
}

// IsSeeded returns true if the store has already been seeded.
func (s *inMemoryCatalogStore) IsSeeded(ctx context.Context) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seeded, nil
}

// SetSeeded marks the store as seeded.
func (s *inMemoryCatalogStore) SetSeeded(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seeded = true
	return nil
}

// Seed inserts the static FodmapDB map into the in-memory store, skipping duplicates.
func (s *inMemoryCatalogStore) Seed(ctx context.Context, items map[string]data.FodmapEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, entry := range items {
		key := strings.ToLower(strings.TrimSpace(name))
		if _, exists := s.items[key]; exists {
			continue
		}
		s.items[key] = store.CatalogEntry{
			Ingredient:    key,
			Level:         entry.Level,
			Groups:        entry.Groups,
			Notes:         entry.Notes,
			Substitutions: entry.Substitutions,
			UpdatedAt:     time.Now().Format(time.RFC3339),
		}
	}
	s.seeded = true
	return nil
}

// filteredItems returns a copy of the filtered slice. Caller must hold at least a read lock.
func (s *inMemoryCatalogStore) filteredItems(filter store.ListFilter) []store.CatalogEntry {
	var out []store.CatalogEntry
	search := strings.TrimSpace(filter.Search)
	for _, e := range s.items {
		if filter.Level != "" && e.Level != filter.Level {
			continue
		}
		if filter.Group != "" && !contains(e.Groups, filter.Group) {
			continue
		}
		if search != "" &&
			!strings.Contains(strings.ToLower(e.Ingredient), strings.ToLower(search)) &&
			!strings.Contains(strings.ToLower(e.Notes), strings.ToLower(search)) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func contains(slice []string, value string) bool {
	for _, s := range slice {
		if s == value {
			return true
		}
	}
	return false
}

// compile-time interface check.
var _ CatalogStore = (*inMemoryCatalogStore)(nil)
