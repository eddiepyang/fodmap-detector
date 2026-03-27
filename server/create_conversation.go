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
	Query string `json:"query"`
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

	bizResult, err := s.searcher.GetBusinesses(r.Context(), req.Query, 1, search.SearchFilter{})
	if err != nil || len(bizResult.Businesses) == 0 {
		respondError(w, "business search failed or no businesses found", http.StatusNotFound)
		return
	}
	b := bizResult.Businesses[0]

	conv := &auth.Conversation{
		ID:         uuid.New().String(),
		UserID:     userID,
		BusinessID: b.ID,
		Title:      "Chat about " + b.Name,
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
