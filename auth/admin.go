package auth

import (
	"context"
)

// UserFilter defines criteria for filtering user listings.
type UserFilter struct {
	Search string `json:"search"`
	Status string `json:"status"` // "active", "suspended", or "" for all
}

// UserDetail holds a user along with counts and their dietary profile.
type UserDetail struct {
	User           *User   `json:"user"`
	Conversations  int     `json:"conversations"`
	Messages       int     `json:"messages"`
	DietaryProfile []byte  `json:"dietary_profile"` // Raw profile JSON or nil
}

// ConversationSummary describes conversation details for administration.
type ConversationSummary struct {
	ID           string `json:"id"`
	UserID       string `json:"user_id"`
	UserEmail    string `json:"user_email"`
	Title        string `json:"title"`
	BusinessID   string `json:"business_id"`
	BusinessName string `json:"business_name"`
	MessageCount int    `json:"message_count"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// UserAnalytics represents dashboard metrics.
type UserAnalytics struct {
	TotalUsers     int     `json:"total_users"`
	ActiveUsers    int     `json:"active_users"`
	SuspendedUsers int     `json:"suspended_users"`
	RecentSignups  []*User `json:"recent_signups"`
}

// ConversationAnalytics represents conversation metrics.
type ConversationAnalytics struct {
	TotalConversations int     `json:"total_conversations"`
	AvgPerUser         float64 `json:"avg_per_user"`
}

// DailyCount represents a day's conversation count.
type DailyCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// AdminStore extends ChatStore with admin-specific operations.
type AdminStore interface {
	ChatStore

	// Role management
	SetUserRole(ctx context.Context, userID string, role string) error

	// User admin
	ListUsers(ctx context.Context, offset, limit int, filter UserFilter) ([]*User, int, error)
	GetUserDetail(ctx context.Context, userID string) (*UserDetail, error)
	DeleteUserPermanently(ctx context.Context, userID string) error
	ResetUserPassword(ctx context.Context, userID string, hashedPassword string) error

	// Conversation admin
	ListAllConversations(ctx context.Context, offset, limit int, search string) ([]*ConversationSummary, int, error)

	// Analytics aggregates
	GetUserAnalytics(ctx context.Context) (*UserAnalytics, error)
	GetConversationActivity(ctx context.Context, days int) ([]DailyCount, error)
	GetConversationAnalytics(ctx context.Context) (*ConversationAnalytics, error)
}
