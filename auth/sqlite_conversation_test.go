package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSQLiteStore_ConversationCRUD(t *testing.T) {
	dbPath := "test_conv.db"
	defer func() { _ = os.Remove(dbPath) }()

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	// Create a user first (foreign key).
	user := &User{
		ID:        uuid.New().String(),
		Email:     "conv-user@example.com",
		Password:  "hashed",
		CreatedAt: time.Now().Truncate(time.Second),
	}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// --- CreateConversation ---
	conv := &Conversation{
		ID:         uuid.New().String(),
		UserID:     user.ID,
		BusinessID: "biz-123",
		Title:      "Test Conversation",
	}
	if err := store.CreateConversation(ctx, conv); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	// --- GetConversation ---
	got, err := store.GetConversation(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if got == nil {
		t.Fatal("GetConversation returned nil")
	}
	if got.Title != "Test Conversation" {
		t.Errorf("Title = %q, want %q", got.Title, "Test Conversation")
	}
	if got.BusinessID != "biz-123" {
		t.Errorf("BusinessID = %q, want %q", got.BusinessID, "biz-123")
	}

	// --- GetConversation non-existent ---
	missing, err := store.GetConversation(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetConversation (missing): %v", err)
	}
	if missing != nil {
		t.Errorf("expected nil for missing conversation, got %+v", missing)
	}

	// --- ListConversations ---
	convs, err := store.ListConversations(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("ListConversations: got %d, want 1", len(convs))
	}
	if convs[0].ID != conv.ID {
		t.Errorf("conversation ID = %q, want %q", convs[0].ID, conv.ID)
	}

	// --- ListConversations for different user ---
	empty, err := store.ListConversations(ctx, "other-user")
	if err != nil {
		t.Fatalf("ListConversations (other user): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 conversations for other user, got %d", len(empty))
	}

	// --- DeleteConversation ---
	if err := store.DeleteConversation(ctx, conv.ID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	deleted, err := store.GetConversation(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetConversation after delete: %v", err)
	}
	if deleted != nil {
		t.Errorf("expected nil after delete, got %+v", deleted)
	}
}

func TestSQLiteStore_MessageCRUD(t *testing.T) {
	dbPath := "test_msg.db"
	defer func() { _ = os.Remove(dbPath) }()

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	// Setup: user + conversation.
	user := &User{ID: uuid.New().String(), Email: "msg-user@example.com", Password: "hashed"}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	conv := &Conversation{ID: uuid.New().String(), UserID: user.ID, BusinessID: "biz-1", Title: "Chat"}
	if err := store.CreateConversation(ctx, conv); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	// --- AddMessage ---
	msg1 := &Message{
		ID:             uuid.New().String(),
		ConversationID: conv.ID,
		Role:           "user",
		Content:        "Hello",
		Sequence:       0,
	}
	msg2 := &Message{
		ID:             uuid.New().String(),
		ConversationID: conv.ID,
		Role:           "model",
		Content:        "Hi there!",
		Sequence:       1,
	}
	if err := store.AddMessage(ctx, msg1); err != nil {
		t.Fatalf("AddMessage (user): %v", err)
	}
	if err := store.AddMessage(ctx, msg2); err != nil {
		t.Fatalf("AddMessage (model): %v", err)
	}

	// --- GetMessages ---
	msgs, err := store.GetMessages(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("GetMessages: got %d, want 2", len(msgs))
	}
	// Verify ordering by sequence.
	if msgs[0].Role != "user" || msgs[1].Role != "model" {
		t.Errorf("message order wrong: [%s, %s]", msgs[0].Role, msgs[1].Role)
	}
	if msgs[0].Content != "Hello" {
		t.Errorf("msg[0].Content = %q, want %q", msgs[0].Content, "Hello")
	}

	// --- GetMessages for empty conversation ---
	empty, err := store.GetMessages(ctx, "nonexistent-conv")
	if err != nil {
		t.Fatalf("GetMessages (empty): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 messages for nonexistent conversation, got %d", len(empty))
	}

	// --- Messages cascade on conversation delete ---
	if err := store.DeleteConversation(ctx, conv.ID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	afterDelete, err := store.GetMessages(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetMessages after delete: %v", err)
	}
	if len(afterDelete) != 0 {
		t.Errorf("expected 0 messages after cascade delete, got %d", len(afterDelete))
	}
}
