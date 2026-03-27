package auth

import "context"

// Store defines the interface for all persistent data (users, conversations, etc.).
type Store interface {
	// User operations
	CreateUser(ctx context.Context, user *User) error
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByID(ctx context.Context, id string) (*User, error)

	// Conversation operations
	CreateConversation(ctx context.Context, conv *Conversation) error
	ListConversations(ctx context.Context, userID string) ([]*Conversation, error)
	GetConversation(ctx context.Context, id string) (*Conversation, error)
	DeleteConversation(ctx context.Context, id string) error

	// Message operations
	AddMessage(ctx context.Context, msg *Message) error
	GetMessages(ctx context.Context, conversationID string) ([]*Message, error)

	Close() error
}
