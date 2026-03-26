package auth

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
)

// PostgresStore implements Store for PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgresStore.
func NewPostgresStore(ctx context.Context, dataSourceName string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("failed to open PostgreSQL database: %w", err)
	}

	// Ping to verify connection
	err = db.PingContext(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	// Create users table if it doesn't exist
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS users (
		id         TEXT PRIMARY KEY,
		email      TEXT UNIQUE NOT NULL,
		password   TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = db.ExecContext(ctx, createTableSQL)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create users table: %w", err)
	}

	return &PostgresStore{db: db}, nil
}

// CreateUser inserts a new user into the database.
func (s *PostgresStore) CreateUser(ctx context.Context, user *User) error {
	query := `INSERT INTO users (id, email, password, created_at) VALUES ($1, $2, $3, $4)`
	_, err := s.db.ExecContext(ctx, query, user.ID, user.Email, user.Password, user.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

// GetUserByEmail retrieves a user by their email address.
func (s *PostgresStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	user := &User{}
	query := `SELECT id, email, password, created_at FROM users WHERE email = $1`
	row := s.db.QueryRowContext(ctx, query, email)
	err := row.Scan(&user.ID, &user.Email, &user.Password, &user.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil // User not found
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user by email: %w", err)
	}
	return user, nil
}

// GetUserByID retrieves a user by their ID.
func (s *PostgresStore) GetUserByID(ctx context.Context, id string) (*User, error) {
	user := &User{}
	query := `SELECT id, email, password, created_at FROM users WHERE id = $1`
	row := s.db.QueryRowContext(ctx, query, id)
	err := row.Scan(&user.ID, &user.Email, &user.Password, &user.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil // User not found
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user by ID: %w", err)
	}
	return user, nil
}

// Close closes the database connection.
func (s *PostgresStore) Close() error {
	return s.db.Close()
}

// SQL compatibility note:
// - TEXT (not VARCHAR)
// - TIMESTAMP DEFAULT CURRENT_TIMESTAMP
/*
CREATE TABLE users (
    id         TEXT PRIMARY KEY,
    email      TEXT UNIQUE NOT NULL,
    password   TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
*/

func (s *PostgresStore) CreateConversation(ctx context.Context, conv *Conversation) error { return nil }
func (s *PostgresStore) ListConversations(ctx context.Context, userID string) ([]*Conversation, error) { return nil, nil }
func (s *PostgresStore) GetConversation(ctx context.Context, id string) (*Conversation, error) { return nil, nil }
func (s *PostgresStore) DeleteConversation(ctx context.Context, id string) error { return nil }
func (s *PostgresStore) AddMessage(ctx context.Context, msg *Message) error { return nil }
func (s *PostgresStore) GetMessages(ctx context.Context, conversationID string) ([]*Message, error) { return nil, nil }
