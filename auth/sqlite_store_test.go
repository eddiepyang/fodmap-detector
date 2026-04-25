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

	// 5. Get by non-existent ID
	missingByID, err := store.GetUserByID(ctx, "nonexistent-id")
	if err != nil {
		t.Fatalf("GetUserByID failed for missing user: %v", err)
	}
	if missingByID != nil {
		t.Errorf("Expected nil for missing ID, got %+v", missingByID)
	}

	// 6. Create duplicate user (email conflict)
	err = store.CreateUser(ctx, &User{
		ID:    uuid.New().String(),
		Email: user.Email, // same email
	})
	if err == nil {
		t.Error("expected error when creating user with duplicate email")
	}

	// 7. Update status
	err = store.UpdateUserStatus(ctx, user.ID, "deleted")
	if err != nil {
		t.Fatalf("UpdateUserStatus failed: %v", err)
	}

	gotDeleted, err := store.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID failed after update: %v", err)
	}
	if gotDeleted == nil || gotDeleted.Status != "deleted" {
		t.Errorf("expected user status to be deleted, got %+v", gotDeleted)
	}

	// 8. Update non-existent user status
	err = store.UpdateUserStatus(ctx, "nonexistent-id", "deleted")
	if err == nil {
		t.Error("expected error when updating status of non-existent user")
	}

	// 9. Dietary Profile
	profileData := []byte(`{"preferences":["vegan"]}`)
	err = store.SaveDietaryProfile(ctx, user.ID, profileData)
	if err != nil {
		t.Fatalf("SaveDietaryProfile failed: %v", err)
	}

	gotProfile, err := store.GetDietaryProfile(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetDietaryProfile failed: %v", err)
	}
	if string(gotProfile) != string(profileData) {
		t.Errorf("expected profile %s, got %s", profileData, gotProfile)
	}

	missingProfile, err := store.GetDietaryProfile(ctx, "nonexistent-id")
	if err != nil {
		t.Fatalf("GetDietaryProfile for missing user failed: %v", err)
	}
	if missingProfile != nil {
		t.Errorf("expected nil profile for missing user, got %s", missingProfile)
	}
}
