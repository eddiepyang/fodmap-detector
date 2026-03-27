package auth

import (
	"time"

	"golang.org/x/crypto/bcrypt"
)

// User represents a registered user in the system.
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Password  string    `json:"-"` // Hashed password
	CreatedAt time.Time `json:"created_at"`
}

// SetPassword hashes the provided plaintext password and stores it in the User.
func (u *User) SetPassword(password string) error {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.Password = string(hashed)
	return nil
}

// CheckPassword verifies if the provided plaintext password matches the stored hash.
func (u *User) CheckPassword(password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password))
	return err == nil
}
