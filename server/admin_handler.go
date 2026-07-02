package server

import (
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"fodmap/auth"
)

// adminRequired validates that the user is an admin.
func (s *Server) adminRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := r.Context().Value(userContextKey).(string)
		if !ok || userID == "" {
			respondError(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		user, err := s.userStore.UserByID(r.Context(), userID)
		if err != nil || user == nil || user.Role != "admin" {
			respondError(w, "forbidden: admin access required", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// adminListUsersHandler lists active/suspended users.
func (s *Server) adminListUsersHandler(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("search")
	status := r.URL.Query().Get("status")
	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("limit")

	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p >= 1 {
			page = p
		}
	}

	limit := 20
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l >= 1 {
			limit = l
		}
	}
	if limit > 100 {
		limit = 100
	}

	offset := (page - 1) * limit

	filter := auth.UserFilter{
		Search: search,
		Status: status,
	}

	users, total, err := s.userStore.ListUsers(r.Context(), offset, limit, filter)
	if err != nil {
		slog.Error("failed to list users", "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"users": users,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// adminGetUserHandler returns detail of a user including counts & dietary profile.
func (s *Server) adminGetUserHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		respondError(w, "missing user id", http.StatusBadRequest)
		return
	}

	detail, err := s.userStore.UserDetail(r.Context(), id)
	if err != nil {
		slog.Error("failed to get user detail", "user_id", id, "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if detail == nil {
		respondError(w, "user not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(detail)
}

// adminUpdateUserStatusHandler updates status (e.g. suspend or unban).
func (s *Server) adminUpdateUserStatusHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		respondError(w, "missing user id", http.StatusBadRequest)
		return
	}

	callingAdminID, _ := r.Context().Value(userContextKey).(string)
	if id == callingAdminID {
		respondError(w, "cannot suspend yourself", http.StatusBadRequest)
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Status != "active" && req.Status != "suspended" {
		respondError(w, "invalid status", http.StatusBadRequest)
		return
	}

	if err := s.userStore.UpdateUserStatus(r.Context(), id, req.Status); err != nil {
		slog.Error("failed to update user status", "user_id", id, "status", req.Status, "error", err)
		respondError(w, "user not found or update failed", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "status updated successfully"})
}

// adminDeleteUserHandler permanently deletes a user.
func (s *Server) adminDeleteUserHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		respondError(w, "missing user id", http.StatusBadRequest)
		return
	}

	callingAdminID, _ := r.Context().Value(userContextKey).(string)
	if id == callingAdminID {
		respondError(w, "cannot delete yourself", http.StatusBadRequest)
		return
	}

	if err := s.userStore.DeleteUserPermanently(r.Context(), id); err != nil {
		slog.Error("failed to delete user", "user_id", id, "error", err)
		respondError(w, "user not found or deletion failed", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "user deleted permanently"})
}

// adminResetPasswordHandler resets password to random temporary code.
func (s *Server) adminResetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		respondError(w, "missing user id", http.StatusBadRequest)
		return
	}

	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
	letterLen := byte(len(letters))
	// Compute rejection ceiling for uniform selection: max byte = 255, 256/letterLen remainder
	rejectCeil := 256 - (256 % int(letterLen))
	tempBytes := make([]byte, 16)
	generated := make([]byte, 0, 16)
	for len(generated) < 16 {
		if _, err := rand.Read(tempBytes); err != nil {
			slog.Error("failed to generate random bytes for password", "error", err)
			respondError(w, "internal server error", http.StatusInternalServerError)
			return
		}
		for _, b := range tempBytes {
			if int(b) < rejectCeil {
				generated = append(generated, letters[b%letterLen])
				if len(generated) == 16 {
					break
				}
			}
		}
	}
	tempPassword := string(generated)

	u := &auth.User{}
	if err := u.SetPassword(tempPassword); err != nil {
		slog.Error("failed to hash password", "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := s.userStore.ResetUserPassword(r.Context(), id, u.Password); err != nil {
		slog.Error("failed to save reset password", "user_id", id, "error", err)
		respondError(w, "user not found or reset failed", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"temporary_password": tempPassword,
	})
}

// adminListConversationsHandler lists conversations across all users.
func (s *Server) adminListConversationsHandler(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("search")
	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("limit")

	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p >= 1 {
			page = p
		}
	}

	limit := 20
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l >= 1 {
			limit = l
		}
	}
	if limit > 100 {
		limit = 100
	}

	offset := (page - 1) * limit

	convs, total, err := s.userStore.ListAllConversations(r.Context(), offset, limit, search)
	if err != nil {
		slog.Error("failed to list conversations", "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"conversations": convs,
		"total":         total,
		"page":          page,
		"limit":         limit,
	})
}

// adminGetConversationHandler returns detailed messages of a conversation.
func (s *Server) adminGetConversationHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		respondError(w, "missing conversation id", http.StatusBadRequest)
		return
	}

	conv, err := s.userStore.Conversation(r.Context(), id)
	if err != nil {
		slog.Error("failed to get conversation", "conv_id", id, "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if conv == nil {
		respondError(w, "conversation not found", http.StatusNotFound)
		return
	}

	messages, err := s.userStore.Messages(r.Context(), id)
	if err != nil {
		slog.Error("failed to get messages", "conv_id", id, "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"conversation": conv,
		"messages":     messages,
	})
}

// adminAnalyticsOverviewHandler aggregates counts and recent signups.
func (s *Server) adminAnalyticsOverviewHandler(w http.ResponseWriter, r *http.Request) {
	userAnalytics, err := s.userStore.UserAnalytics(r.Context())
	if err != nil {
		slog.Error("failed to get user analytics", "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	convAnalytics, err := s.userStore.ConversationAnalytics(r.Context())
	if err != nil {
		slog.Error("failed to get conversation analytics", "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"total_users":         userAnalytics.TotalUsers,
		"active_users":        userAnalytics.ActiveUsers,
		"suspended_users":     userAnalytics.SuspendedUsers,
		"recent_signups":      userAnalytics.RecentSignups,
		"total_conversations": convAnalytics.TotalConversations,
		"avg_conversations":   convAnalytics.AvgPerUser,
	})
}

// adminConversationActivityHandler returns day-by-day conversation activity chart data.
func (s *Server) adminConversationActivityHandler(w http.ResponseWriter, r *http.Request) {
	daysStr := r.URL.Query().Get("days")
	days := 30
	if daysStr != "" {
		if d, err := strconv.Atoi(daysStr); err == nil && d >= 1 {
			days = d
		}
	}
	if days > 90 {
		days = 90
	}

	activity, err := s.userStore.ConversationActivity(r.Context(), days)
	if err != nil {
		slog.Error("failed to get conversation activity", "error", err)
		respondError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(activity)
}

func (s *Server) adminListMenuItemsHandler(w http.ResponseWriter, r *http.Request) {
	var ms MenuStore
	if s.menuStore != nil {
		ms = s.menuStore
	} else if cast, ok := s.searcher.(MenuStore); ok {
		ms = cast
	}

	if ms == nil {
		respondError(w, "menu store not configured", http.StatusNotImplemented)
		return
	}

	search := r.URL.Query().Get("search")
	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("limit")

	page := 1
	limit := 50
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
		limit = l
	}
	offset := (page - 1) * limit

	items, total, err := ms.ListMenuItems(r.Context(), search, limit, offset)
	if err != nil {
		slog.Error("admin list menu items", "err", err)
		respondError(w, "failed to list menu items", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items": items,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}
