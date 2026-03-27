package server

import (
	"context"
	"errors"
	"testing"

	"fodmap/search"
	"fodmap/data"
)

// MockSearcher is a mock implementation of the Searcher interface for testing.
type MockSearcher struct {
	FodmapResult *search.FodmapResult
	Err          error
}

func (m *MockSearcher) GetBusinesses(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchResult, error) {
	return search.SearchResult{}, nil
}

func (m *MockSearcher) GetReviews(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchReviews, error) {
	return search.SearchReviews{}, nil
}

func (m *MockSearcher) SearchFodmap(ctx context.Context, ingredient string) (search.FodmapResult, float64, error) {
	if m.Err != nil {
		return search.FodmapResult{}, 0, m.Err
	}
	if m.FodmapResult != nil {
		return *m.FodmapResult, 0, nil
	}
	return search.FodmapResult{}, 0, nil
}

func (m *MockSearcher) EnsureSchema(ctx context.Context) error {
	return nil
}

func (m *MockSearcher) EnsureFodmapSchema(ctx context.Context) error {
	return nil
}

func (m *MockSearcher) BatchUpsertFodmap(ctx context.Context, _ map[string]data.FodmapEntry) error {
	return m.Err
}

func TestDirectFodmapClient_LookupFODMAP(t *testing.T) {
	testCases := []struct {
		name          string
		searcher      *MockSearcher
		ingredient    string
		expectedFound bool
		expectedLevel string
		expectedErr   bool
	}{
		{
			name: "FODMAP found",
			searcher: &MockSearcher{
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
			searcher:      &MockSearcher{},
			ingredient:    "unknown",
			expectedFound: false,
			expectedErr:   false,
		},
		{
			name:          "Search error",
			searcher:      &MockSearcher{Err: errors.New("search unavailable")},
			ingredient:    "garlic",
			expectedFound: false,
			expectedErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{searcher: tc.searcher}
			client := NewDirectFodmapClient(s)

			result, err := client.LookupFODMAP(context.Background(), tc.ingredient)

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
