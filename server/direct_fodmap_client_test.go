package server

import (
	"context"
	"errors"
	"testing"

	"fodmap/data"
	"fodmap/search"
)

// StubSearcher is a stub implementation of the Searcher interface for testing.
type StubSearcher struct {
	FodmapResult *search.FodmapResult
	Err          error
}

func (m *StubSearcher) Businesses(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchResult, error) {
	return search.SearchResult{}, m.Err
}
func (m *StubSearcher) Reviews(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchReviews, error) {
	return search.SearchReviews{}, m.Err
}
func (m *StubSearcher) SearchFodmap(ctx context.Context, ingredient string) (search.FodmapResult, float64, error) {
	if m.FodmapResult != nil {
		return *m.FodmapResult, 1.0, m.Err
	}
	return search.FodmapResult{}, 0, m.Err
}
func (m *StubSearcher) EnsureSchema(ctx context.Context) error {
	return nil
}
func (m *StubSearcher) EnsureFodmapSchema(ctx context.Context) error {
	return nil
}
func (m *StubSearcher) BatchUpsertFodmap(ctx context.Context, items map[string]data.FodmapEntry) error {
	return m.Err
}
func (m *StubSearcher) BatchUpsert(ctx context.Context, items []search.IndexItem) error {
	return m.Err
}

func TestDirectFodmapClient_LookupFodmap(t *testing.T) {
	testCases := []struct {
		name          string
		searcher      *StubSearcher
		ingredient    string
		expectedFound bool
		expectedLevel string
		expectedErr   bool
	}{
		{
			name: "FODMAP found",
			searcher: &StubSearcher{
				FodmapResult: &search.FodmapResult{
					Ingredient: "garlic",
					Level:      "High",
				},
			},
			ingredient:    "garlic",
			expectedFound: true,
			expectedLevel: "High",
			expectedErr:   false,
		},
		{
			name:          "FODMAP not found",
			searcher:      &StubSearcher{},
			ingredient:    "unknown",
			expectedFound: false,
			expectedErr:   false,
		},
		{
			name:          "Search error",
			searcher:      &StubSearcher{Err: errors.New("search unavailable")},
			ingredient:    "garlic",
			expectedFound: false,
			expectedErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{searcher: tc.searcher}
			client := NewDirectFodmapClient(s)

			result, err := client.LookupFodmap(context.Background(), tc.ingredient)

			if (err != nil) != tc.expectedErr {
				t.Fatalf("expected error: %v, got: %v", tc.expectedErr, err)
			}
			if result.Found != tc.expectedFound {
				t.Errorf("expected found: %v, got: %v", tc.expectedFound, result.Found)
			}
			if result.FodmapLevel != tc.expectedLevel {
				t.Errorf("expected level: %v, got: %v", tc.expectedLevel, result.FodmapLevel)
			}
		})
	}
}
