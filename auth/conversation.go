package auth

import "time"

// Conversation represents a chat session between a user and the model.
type Conversation struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	BusinessID  string    `json:"business_id"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Message represents a single turn in a conversation.
type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Role           string    `json:"role"`    // "user", "model", "tool_call", "tool_response"
	Content        string    `json:"content"` // JSON for tool-related messages
	Sequence       int       `json:"sequence"`
	CreatedAt      time.Time `json:"created_at"`
}
