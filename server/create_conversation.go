package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"fodmap/auth"
	"fodmap/search"

	"github.com/google/uuid"
)

type createConversationRequest struct {
	Query             string `json:"query"`
	BusinessID        string `json:"business_id"`
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
		// We should probably fetch the name too for the title, 
		// but for now we can just search for it or use a default.
		bizResult, err := s.searcher.GetBusinesses(r.Context(), "", 1, search.SearchFilter{BusinessID: businessID})
		if err == nil && len(bizResult.Businesses) > 0 {
			businessName = bizResult.Businesses[0].Name
		} else {
			businessName = "Restaurant"
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
		slog.Error("failed to create conversation", "error", err)
		respondError(w, "failed to create conversation", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"conversation": conv})
}
