package auth

import "time"

// Conversation represents a chat session between a user and the model.
type Conversation struct {
	ID                string        `json:"id"`
	UserID            string        `json:"user_id"`
	BusinessID        string        `json:"business_id"`
	Title             string        `json:"title"`
	SearchCategory    string        `json:"search_category,omitempty"`
	SearchCity        string        `json:"search_city,omitempty"`
	SearchState       string        `json:"search_state,omitempty"`
	SearchDescription string        `json:"search_description,omitempty"`
	ReviewContext     []ReviewScore `json:"review_context,omitempty"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

// ReviewScore pairs a review ID with its original search certainty score.
type ReviewScore struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
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
