package server

import (
	"encoding/json"
	"net/http"

	"fodmap/auth"
)

func (s *Server) listConversationsHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userContextKey).(string)
	if !ok {
		respondError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	convs, err := s.userStore.ListConversations(r.Context(), userID)
	if err != nil {
		respondError(w, "failed to list conversations", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(convs)
}

func (s *Server) getConversationHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userContextKey).(string)
	if !ok {
		respondError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	convID := r.PathValue("id")
	conv, err := s.userStore.GetConversation(r.Context(), convID)
	if err != nil {
		respondError(w, "failed to load conversation", http.StatusInternalServerError)
		return
	}
	if conv == nil {
		respondError(w, "conversation not found", http.StatusNotFound)
		return
	}

	if conv.UserID != userID {
		respondError(w, "forbidden", http.StatusForbidden)
		return
	}

	messages, err := s.userStore.GetMessages(r.Context(), convID)
	if err != nil {
		respondError(w, "failed to load messages", http.StatusInternalServerError)
		return
	}

	resp := struct {
		Conversation *auth.Conversation `json:"conversation"`
		Messages     []*auth.Message    `json:"messages"`
	}{
		Conversation: conv,
		Messages:     messages,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) deleteConversationHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userContextKey).(string)
	if !ok {
		respondError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	convID := r.PathValue("id")
	conv, err := s.userStore.GetConversation(r.Context(), convID)
	if err != nil {
		respondError(w, "failed to load conversation", http.StatusInternalServerError)
		return
	}
	if conv == nil {
		respondError(w, "conversation not found", http.StatusNotFound)
		return
	}

	if conv.UserID != userID {
		respondError(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := s.userStore.DeleteConversation(r.Context(), convID); err != nil {
		respondError(w, "failed to delete conversation", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
