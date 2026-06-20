package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"fodmap/auth"

	"github.com/google/uuid"
)

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authUserResponse struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Status string `json:"status"`
}

type authResponse struct {
	AccessToken  string           `json:"access_token"`
	Token        string           `json:"token"` // Alias for frontend compatibility
	RefreshToken string           `json:"refresh_token"`
	User         authUserResponse `json:"user"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) registerHandler(w http.ResponseWriter, r *http.Request) {
	if s.userStore == nil {
		respondError(w, "authentication is not enabled", http.StatusServiceUnavailable)
		return
	}

	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Email == "" || req.Password == "" {
		respondError(w, "email and password are required", http.StatusBadRequest)
		return
	}
	if len(req.Password) < 8 {
		respondError(w, "password must be at least 8 characters", http.StatusBadRequest)
		return
	}

	user := &auth.User{
		ID:     uuid.New().String(),
		Email:  req.Email,
		Role:   "user",
		Status: "active",
	}
	if err := user.SetPassword(req.Password); err != nil {
		respondError(w, "failed to process password", http.StatusInternalServerError)
		return
	}

	if err := s.userStore.CreateUser(r.Context(), user); err != nil {
		slog.Warn("failed to create user", "email", req.Email, "error", err)
		respondError(w, "user already exists or could not be created", http.StatusConflict)
		return
	}

	if s.adminEmail != "" && user.Email == s.adminEmail {
		if err := s.userStore.SetUserRole(r.Context(), user.ID, "admin"); err != nil {
			slog.Warn("failed to auto-promote admin user", "email", user.Email, "error", err)
		} else {
			user.Role = "admin"
		}
	}

	// Generate tokens for automatic initial login after registration
	access, refresh, err := auth.GenerateTokensWithRole(user.ID, user.Role, s.jwtSecret)
	if err != nil {
		slog.Error("user created but token generation failed", "user_id", user.ID, "error", err)
		respondError(w, "account created but login failed; please sign in manually", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(authResponse{
		AccessToken:  access,
		Token:        access,
		RefreshToken: refresh,
		User: authUserResponse{
			ID:     user.ID,
			Email:  user.Email,
			Role:   user.Role,
			Status: user.Status,
		},
	})
}

func (s *Server) loginHandler(w http.ResponseWriter, r *http.Request) {
	if s.userStore == nil {
		respondError(w, "authentication is not enabled", http.StatusServiceUnavailable)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	user, err := s.userStore.UserByEmail(r.Context(), req.Email)
	if err != nil || user == nil {
		respondError(w, "no account found for this email", http.StatusUnauthorized)
		return
	}

	if !user.CheckPassword(req.Password) {
		respondError(w, "incorrect password", http.StatusUnauthorized)
		return
	}

	if user.Status == "deleted" || user.Status == "suspended" {
		respondError(w, "account is "+user.Status, http.StatusUnauthorized)
		return
	}

	access, refresh, err := auth.GenerateTokensWithRole(user.ID, user.Role, s.jwtSecret)
	if err != nil {
		respondError(w, "failed to generate tokens", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(authResponse{
		AccessToken:  access,
		Token:        access,
		RefreshToken: refresh,
		User: authUserResponse{
			ID:     user.ID,
			Email:  user.Email,
			Role:   user.Role,
			Status: user.Status,
		},
	})
}

func (s *Server) refreshHandler(w http.ResponseWriter, r *http.Request) {
	if s.jwtSecret == "" {
		respondError(w, "authentication is not enabled", http.StatusServiceUnavailable)
		return
	}

	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	claims, err := auth.ValidateToken(req.RefreshToken, s.jwtSecret)
	if err != nil {
		respondError(w, "invalid or expired refresh token", http.StatusUnauthorized)
		return
	}

	var user *auth.User
	if s.userStore != nil {
		user, err = s.userStore.UserByID(r.Context(), claims.UserID)
		if err != nil || user == nil {
			respondError(w, "user not found", http.StatusUnauthorized)
			return
		}
		if user.Status == "deleted" || user.Status == "suspended" {
			respondError(w, "account is "+user.Status, http.StatusUnauthorized)
			return
		}
	}

	// Default role to "user" if userStore is nil
	role := "user"
	if user != nil {
		role = user.Role
	}

	access, refresh, err := auth.GenerateTokensWithRole(claims.UserID, role, s.jwtSecret)
	if err != nil {
		respondError(w, "failed to generate tokens", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(authResponse{
		AccessToken:  access,
		RefreshToken: refresh,
	})
}

func (s *Server) logoutHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "logged out"})
}

func (s *Server) deleteUserHandler(w http.ResponseWriter, r *http.Request) {
	if s.userStore == nil {
		respondError(w, "authentication is not enabled", http.StatusServiceUnavailable)
		return
	}

	userID, ok := r.Context().Value(userContextKey).(string)
	if !ok || userID == "" {
		respondError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := s.userStore.UpdateUserStatus(r.Context(), userID, "deleted"); err != nil {
		slog.Warn("failed to delete user", "user_id", userID, "error", err)
		respondError(w, "failed to delete account", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "account deleted"})
}

func (s *Server) meHandler(w http.ResponseWriter, r *http.Request) {
	if s.userStore == nil {
		respondError(w, "authentication is not enabled", http.StatusServiceUnavailable)
		return
	}

	userID, ok := r.Context().Value(userContextKey).(string)
	if !ok || userID == "" {
		respondError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := s.userStore.UserByID(r.Context(), userID)
	if err != nil || user == nil || user.Status == "deleted" {
		respondError(w, "user not found", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(authUserResponse{
		ID:     user.ID,
		Email:  user.Email,
		Role:   user.Role,
		Status: user.Status,
	})
}

func respondError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
