package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"fodmap/auth"
	"fodmap/search"
)

func TestCreateConversationHandler_Metadata(t *testing.T) {
	store := newMockStore()
	mockSearcher := &chatMockSearcher{}
	s := &Server{
		userStore: store,
		searcher:  mockSearcher,
	}

	reqBody, _ := json.Marshal(map[string]string{
		"query":              "pad thai",
		"business_id":       "biz-1",
		"search_category":    "Thai",
		"search_city":        "San Francisco",
		"search_state":       "CA",
		"search_description": "best pad thai",
	})
	req := httptest.NewRequest(http.MethodPost, "/conversations", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	s.createConversationHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var resp struct {
		Conversation *auth.Conversation `json:"conversation"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	conv := resp.Conversation
	if conv.SearchCategory != "Thai" {
		t.Errorf("SearchCategory = %q, want %q", conv.SearchCategory, "Thai")
	}
	if conv.SearchCity != "San Francisco" {
		t.Errorf("SearchCity = %q, want %q", conv.SearchCity, "San Francisco")
	}
	if conv.SearchDescription != "best pad thai" {
		t.Errorf("SearchDescription = %q, want %q", conv.SearchDescription, "best pad thai")
	}
}

func TestCreateConversationHandler_ReviewLimit(t *testing.T) {
	store := newMockStore()
	mockSearcher := &limitMockSearcher{}
	s := &Server{
		userStore: store,
		searcher:  mockSearcher,
	}

	reqBody, _ := json.Marshal(map[string]string{
		"query": "test",
		"business_id": "biz-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/conversations", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	s.createConversationHandler(rec, req)

	if mockSearcher.LastLimit != 10 {
		t.Errorf("GetReviews limit = %d, want 10", mockSearcher.LastLimit)
	}
}

type limitMockSearcher struct {
	chatMockSearcher
	LastLimit int
}

func (m *limitMockSearcher) GetReviews(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchReviews, error) {
	m.LastLimit = limit
	return m.chatMockSearcher.GetReviews(ctx, query, limit, filter)
}
