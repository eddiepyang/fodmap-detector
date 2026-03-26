package auth

import (
	"testing"
	"time"
)

func TestJWT(t *testing.T) {
	secret := "test-secret"
	userID := "user-123"

	// 1. Generate tokens
	accessToken, refreshToken, err := GenerateTokens(userID, secret)
	if err != nil {
		t.Fatalf("GenerateTokens failed: %v", err)
	}

	if accessToken == "" || refreshToken == "" {
		t.Errorf("Empty tokens generated")
	}

	// 2. Validate access token
	claims, err := ValidateToken(accessToken, secret)
	if err != nil {
		t.Fatalf("ValidateToken (access) failed: %v", err)
	}
	if claims.UserID != userID {
		t.Errorf("UserID mismatch: got %s, want %s", claims.UserID, userID)
	}
	if claims.ExpiresAt.Before(time.Now()) {
		t.Errorf("Access token already expired")
	}

	// 3. Validate refresh token
	refreshClaims, err := ValidateToken(refreshToken, secret)
	if err != nil {
		t.Fatalf("ValidateToken (refresh) failed: %v", err)
	}
	if refreshClaims.UserID != userID {
		t.Errorf("UserID mismatch in refresh: got %s, want %s", refreshClaims.UserID, userID)
	}

	// 4. Validate with wrong secret
	_, err = ValidateToken(accessToken, "wrong-secret")
	if err == nil {
		t.Errorf("ValidateToken succeeded with wrong secret")
	}
}
