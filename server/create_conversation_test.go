package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"fodmap/auth"
	"fodmap/data/schemas"
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
		"business_id":        "biz-1",
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
		"query":       "test",
		"business_id": "biz-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/conversations", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	s.createConversationHandler(rec, req)

	if mockSearcher.FirstLimit != 10 {
		t.Errorf("GetReviews limit = %d, want 10", mockSearcher.FirstLimit)
	}
}

type limitMockSearcher struct {
	chatMockSearcher
	FirstLimit int
	calls      int
}

func (m *limitMockSearcher) GetReviews(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchReviews, error) {
	m.calls++
	if m.calls == 1 {
		m.FirstLimit = limit
	}
	return m.chatMockSearcher.GetReviews(ctx, query, limit, filter)
}

// reviewMockSearcher returns reviews with ReviewIDs so that
// createConversationHandler populates ReviewContext and generates a summary.
type reviewMockSearcher struct {
	chatMockSearcher
}

func (m *reviewMockSearcher) GetReviews(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchReviews, error) {
	return search.SearchReviews{
		BusinessReviews: []search.RankedReview{
			{
				Score: 0.9,
				Review: search.IndexItem{
					Review: schemas.Review{ReviewID: "rev-1", BusinessID: "biz-1", Stars: 4, Text: "Great pad thai"},
				},
			},
			{
				Score: 0.8,
				Review: search.IndexItem{
					Review: schemas.Review{ReviewID: "rev-2", BusinessID: "biz-1", Stars: 5, Text: "Amazing green curry"},
				},
			},
		},
	}, nil
}

func TestCreateConversationHandler_SummaryPending(t *testing.T) {
	store := newMockStore()
	s := &Server{
		userStore: store,
		searcher:  &reviewMockSearcher{},
		// genaiClient is nil — background goroutine will use FormatReviewsContext.
	}

	reqBody, _ := json.Marshal(map[string]string{
		"business_id":   "biz-1",
		"business_name": "Thai Palace",
		"query":         "pad thai",
	})
	req := httptest.NewRequest(http.MethodPost, "/conversations", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	s.createConversationHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var resp struct {
		Conversation   *auth.Conversation `json:"conversation"`
		SummaryPending bool               `json:"summary_pending"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !resp.SummaryPending {
		t.Error("expected summary_pending=true when reviews exist")
	}

	// Wait briefly for the background goroutine to finish.
	time.Sleep(200 * time.Millisecond)

	msgs, err := store.GetMessages(context.Background(), resp.Conversation.ID)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("stored messages = %d, want 1", len(msgs))
	}
	if msgs[0].Role != "model" {
		t.Errorf("stored message role = %q, want %q", msgs[0].Role, "model")
	}
}

func TestCreateConversationHandler_NoReviews_NoSummary(t *testing.T) {
	store := newMockStore()
	s := &Server{
		userStore: store,
		searcher:  &emptyReviewSearcher{},
	}

	reqBody, _ := json.Marshal(map[string]string{
		"business_id":   "biz-1",
		"business_name": "Empty Bistro",
		"query":         "nothing",
	})
	req := httptest.NewRequest(http.MethodPost, "/conversations", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	s.createConversationHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var resp struct {
		Conversation   *auth.Conversation `json:"conversation"`
		SummaryPending bool               `json:"summary_pending"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SummaryPending {
		t.Error("expected summary_pending=false when no reviews")
	}

	msgs, _ := store.GetMessages(context.Background(), resp.Conversation.ID)
	if len(msgs) != 0 {
		t.Errorf("stored messages = %d, want 0", len(msgs))
	}
}

// emptyReviewSearcher returns businesses but empty reviews.
type emptyReviewSearcher struct {
	chatMockSearcher
}

func (m *emptyReviewSearcher) GetReviews(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchReviews, error) {
	return search.SearchReviews{}, nil
}
