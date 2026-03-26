package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSQLiteStore(t *testing.T) {
	dbPath := "test_users.db"
	defer func() { _ = os.Remove(dbPath) }()

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	user := &User{
		ID:        uuid.New().String(),
		Email:     "user@example.com",
		Password:  "hashed-password",
		CreatedAt: time.Now().Truncate(time.Second), // SQLite timestamp precision
	}

	// 1. Create user
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// 2. Get by Email
	got, err := store.GetUserByEmail(ctx, user.Email)
	if err != nil {
		t.Fatalf("GetUserByEmail failed: %v", err)
	}
	if got == nil {
		t.Fatalf("User not found by email")
	}
	if got.ID != user.ID || got.Email != user.Email {
		t.Errorf("User mismatch: got %+v, want %+v", got, user)
	}

	// 3. Get by ID
	gotByID, err := store.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}
	if gotByID == nil {
		t.Fatalf("User not found by ID")
	}
	if gotByID.ID != user.ID {
		t.Errorf("ID mismatch: got %s, want %s", gotByID.ID, user.ID)
	}

	// 4. Get non-existent user
	missing, err := store.GetUserByEmail(ctx, "nonexistent@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail failed for missing user: %v", err)
	}
	if missing != nil {
		t.Errorf("Expected nil for missing user, got %+v", missing)
	}
}
