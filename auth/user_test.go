package auth

import (
	"testing"
)

func TestUserPassword(t *testing.T) {
	u := &User{Email: "test@example.com"}
	password := "secure-password"

	// 1. Set password
	if err := u.SetPassword(password); err != nil {
		t.Fatalf("SetPassword failed: %v", err)
	}

	// 2. Hash should not be the plaintext password
	if u.Password == password {
		t.Errorf("Password was not hashed")
	}

	// 3. Validation should succeed with correct password
	if !u.CheckPassword(password) {
		t.Errorf("CheckPassword failed with correct password")
	}

	// 4. Validation should fail with wrong password
	if u.CheckPassword("wrong-password") {
		t.Errorf("CheckPassword succeeded with wrong password")
	}
}
