package server

import (
	"encoding/json"
	"net/http"
	"fodmap/chat"
	"log/slog"
)

type profileRequest struct {
	Input string `json:"input"`
}

func (s *Server) updateProfileHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userContextKey).(string)
	if !ok || userID == "" {
		respondError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req profileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Input == "" {
		respondError(w, "input is required", http.StatusBadRequest)
		return
	}

	user, err := s.userStore.GetUserByID(r.Context(), userID)
	if err != nil || user == nil {
		respondError(w, "user not found", http.StatusNotFound)
		return
	}

	// Generate profile using Gemini
	if s.genaiClient == nil {
		respondError(w, "chat service not configured", http.StatusServiceUnavailable)
		return
	}

	profileJSON, err := chat.GenerateDietaryProfile(r.Context(), s.genaiClient, s.chatModel, req.Input)
	if err != nil {
		slog.Error("failed to generate profile", "error", err)
		respondError(w, "failed to generate profile", http.StatusInternalServerError)
		return
	}

	if err := s.userStore.SaveDietaryProfile(r.Context(), userID, profileJSON); err != nil {
		slog.Error("failed to update user profile in db", "error", err)
		respondError(w, "failed to save profile", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(profileJSON)
}

func (s *Server) getProfileHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userContextKey).(string)
	if !ok || userID == "" {
		respondError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	profile, err := s.userStore.GetDietaryProfile(r.Context(), userID)
	if err != nil {
		respondError(w, "user profile fetch failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if len(profile) > 0 {
		w.Write(profile)
	} else {
		w.Write([]byte("{}"))
	}
}
