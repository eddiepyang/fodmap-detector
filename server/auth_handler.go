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
	ID    string `json:"id"`
	Email string `json:"email"`
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
		ID:    uuid.New().String(),
		Email: req.Email,
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

	// Generate tokens for automatic initial login after registration
	access, refresh, err := auth.GenerateTokens(user.ID, s.jwtSecret)
	if err != nil {
		// User is created but tokens failed; return success with error message?
		// No, return 201 Created and let login handle it or just return tokens now.
		slog.Warn("user created but token generation failed", "user_id", user.ID, "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(authResponse{
		AccessToken:  access,
		Token:        access,
		RefreshToken: refresh,
		User: authUserResponse{
			ID:    user.ID,
			Email: user.Email,
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

	user, err := s.userStore.GetUserByEmail(r.Context(), req.Email)
	if err != nil || user == nil {
		respondError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	if !user.CheckPassword(req.Password) {
		respondError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	access, refresh, err := auth.GenerateTokens(user.ID, s.jwtSecret)
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
			ID:    user.ID,
			Email: user.Email,
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

	if s.userStore != nil {
		user, err := s.userStore.GetUserByID(r.Context(), claims.UserID)
		if err != nil || user == nil {
			respondError(w, "user not found", http.StatusUnauthorized)
			return
		}
	}

	access, refresh, err := auth.GenerateTokens(claims.UserID, s.jwtSecret)
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

func respondError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
