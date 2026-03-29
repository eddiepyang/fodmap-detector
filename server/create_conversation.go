package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"fodmap/auth"
	"fodmap/chat"

	"fodmap/search"

	"github.com/google/uuid"
)

type createConversationRequest struct {
	Query             string `json:"query"`
	BusinessID        string `json:"business_id"`
	BusinessName      string `json:"business_name"`
	SearchCategory    string `json:"search_category"`
	SearchCity        string `json:"search_city"`
	SearchState       string `json:"search_state"`
	SearchDescription string `json:"search_description"`
}

func (s *Server) createConversationHandler(w http.ResponseWriter, r *http.Request) {
	var req createConversationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	userID, _ := r.Context().Value(userContextKey).(string)
	if userID == "" {
		userID = "anonymous"
	}

	var businessID string
	var businessName string

	if req.BusinessID != "" {
		// Use provided business ID
		businessID = req.BusinessID
		if req.BusinessName != "" {
			businessName = req.BusinessName
		} else {
			// Fallback: fetch the name if not provided
			bizResult, err := s.searcher.GetBusinesses(r.Context(), "", 1, search.SearchFilter{BusinessID: businessID})
			if err == nil && len(bizResult.Businesses) > 0 {
				businessName = bizResult.Businesses[0].Name
			} else {
				businessName = "Restaurant"
			}
		}
	} else {
		// Search based on query
		bizResult, err := s.searcher.GetBusinesses(r.Context(), req.Query, 1, search.SearchFilter{})
		if err != nil || len(bizResult.Businesses) == 0 {
			respondError(w, "business search failed or no businesses found", http.StatusNotFound)
			return
		}
		businessID = bizResult.Businesses[0].ID
		businessName = bizResult.Businesses[0].Name
	}

	conv := &auth.Conversation{
		ID:                uuid.New().String(),
		UserID:            userID,
		BusinessID:        businessID,
		BusinessName:      businessName,
		Title:             "Chat about " + businessName,
		SearchCategory:    req.SearchCategory,
		SearchCity:        req.SearchCity,
		SearchState:       req.SearchState,
		SearchDescription: req.SearchDescription,
	}

	// Capture the specific reviews and scores for this context.
	query := req.Query
	if query == "" {
		query = req.SearchDescription
	}
	if query == "" {
		query = "menu and food" // fallback for broad context
	}
	reviewResult, err := s.searcher.GetReviews(r.Context(), query, 10, search.SearchFilter{BusinessID: businessID})
	if err == nil {
		for _, rr := range reviewResult.BusinessReviews {
			conv.ReviewContext = append(conv.ReviewContext, auth.ReviewScore{
				ID:    rr.Review.Review.ReviewID,
				Score: rr.Score,
			})
		}
	}
	if err := s.userStore.CreateConversation(r.Context(), conv); err != nil {
		slog.Error("Failed to create conversation in DB", "error", err, "business_id", businessID, "user_id", userID)
		respondError(w, "failed to create conversation", http.StatusInternalServerError)
		return
	}

	// Generate the review summary in the background so the response returns
	// immediately. The frontend polls GET /conversations/{id} until the
	// initial message appears.
	if len(conv.ReviewContext) > 0 {
		ids := make([]string, len(conv.ReviewContext))
		for i, rc := range conv.ReviewContext {
			ids[i] = rc.ID
		}
		go s.generateReviewSummary(conv.ID, businessName, ids)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"conversation":    conv,
		"summary_pending": len(conv.ReviewContext) > 0,
	}); err != nil {
		slog.Error("Failed to encode conversation response", "error", err)
	}
}

// generateReviewSummary fetches review text and stores a summarized context
// message in the background. Uses its own context independent of the HTTP request.
func (s *Server) generateReviewSummary(convID, businessName string, reviewIDs []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reviewResult, err := s.searcher.GetReviews(ctx, "", len(reviewIDs), search.SearchFilter{ReviewIDs: reviewIDs})
	if err != nil || len(reviewResult.BusinessReviews) == 0 {
		slog.Warn("background summary: failed to fetch reviews", "error", err, "conv", convID)
		return
	}

	reviews := make([]chat.Review, 0, len(reviewResult.BusinessReviews))
	for _, rr := range reviewResult.BusinessReviews {
		reviews = append(reviews, chat.Review{
			Stars: rr.Review.Review.Stars,
			Text:  rr.Review.Review.Text,
		})
	}

	var contextContent string
	if s.genaiClient != nil {
		contextContent, err = chat.SummarizeReviews(ctx, s.genaiClient, s.chatModel, businessName, reviews)
		if err != nil {
			slog.Warn("background summary: summarization failed, using raw context", "error", err, "conv", convID)
			contextContent = chat.FormatReviewsContext(businessName, reviews)
		}
	} else {
		contextContent = chat.FormatReviewsContext(businessName, reviews)
	}

	msg := &auth.Message{
		ID:             fmt.Sprintf("msg-%s-ctx", convID),
		ConversationID: convID,
		Role:           "model",
		Content:        contextContent,
		Sequence:       0,
		CreatedAt:      time.Now(),
	}
	if err := s.userStore.AddMessage(ctx, msg); err != nil {
		slog.Warn("background summary: failed to save context message", "error", err, "conv", convID)
	} else {
		slog.Info("background summary: saved", "conv", convID)
	}
}
