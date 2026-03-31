package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// SQLiteStore implements the unified Store interface for SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLiteStore and ensures tables are created.
func NewSQLiteStore(dataSourceName string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}

	// Enable foreign key support.
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Initialise the schema.
	schemas := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id         TEXT PRIMARY KEY,
			email      TEXT UNIQUE NOT NULL,
			password   TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS conversations (
			id          TEXT PRIMARY KEY,
			user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			business_id TEXT NOT NULL,
			title       TEXT NOT NULL,
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			id              TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
			role            TEXT NOT NULL,
			content         TEXT NOT NULL,
			sequence        INTEGER NOT NULL,
			created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id, sequence);`,
	}

	for _, s := range schemas {
		if _, err := db.Exec(s); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("error creating schema: %w", err)
		}
	}

	// Add new columns if they don't exist (migrations).
	columns := []string{
		"ALTER TABLE conversations ADD COLUMN review_context TEXT;",
		"ALTER TABLE conversations ADD COLUMN search_category TEXT;",
		"ALTER TABLE conversations ADD COLUMN search_city TEXT;",
		"ALTER TABLE conversations ADD COLUMN search_state TEXT;",
		"ALTER TABLE conversations ADD COLUMN search_description TEXT;",
		"ALTER TABLE conversations ADD COLUMN business_name TEXT;",
	}

	for _, col := range columns {
		if _, err := db.Exec(col); err != nil {
			// Ignore if column already exists (error code varies but text contains "duplicate column name")
			if !strings.Contains(err.Error(), "duplicate column name") {
				slog.Error("Migration error", "query", col, "error", err)
			}
		}
	}

	return &SQLiteStore{db: db}, nil
}

// CreateUser inserts a new user into the database.
func (s *SQLiteStore) CreateUser(ctx context.Context, user *User) error {
	query := `INSERT INTO users (id, email, password, created_at) VALUES (?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query, user.ID, user.Email, user.Password, user.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

// GetUserByEmail retrieves a user by their email address.
func (s *SQLiteStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	user := &User{}
	query := `SELECT id, email, password, created_at FROM users WHERE email = ?`
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
func (s *SQLiteStore) GetUserByID(ctx context.Context, id string) (*User, error) {
	user := &User{}
	query := `SELECT id, email, password, created_at FROM users WHERE id = ?`
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

// CreateConversation inserts a new conversation.
func (s *SQLiteStore) CreateConversation(ctx context.Context, conv *Conversation) error {
	if conv.CreatedAt.IsZero() {
		conv.CreatedAt = time.Now()
	}
	if conv.UpdatedAt.IsZero() {
		conv.UpdatedAt = conv.CreatedAt
	}

	var contextJSON []byte
	var err error
	if len(conv.ReviewContext) > 0 {
		contextJSON, err = json.Marshal(conv.ReviewContext)
		if err != nil {
			return fmt.Errorf("failed to marshal review context: %w", err)
		}
	}

	query := `INSERT INTO conversations (id, user_id, business_id, business_name, title, created_at, updated_at, review_context, search_category, search_city, search_state, search_description) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = s.db.ExecContext(ctx, query, conv.ID, conv.UserID, conv.BusinessID, conv.BusinessName, conv.Title, conv.CreatedAt, conv.UpdatedAt, string(contextJSON), conv.SearchCategory, conv.SearchCity, conv.SearchState, conv.SearchDescription)
	if err != nil {
		return fmt.Errorf("failed to create conversation: %w", err)
	}
	return nil
}

// ListConversations returns all conversations for a user.
func (s *SQLiteStore) ListConversations(ctx context.Context, userID string) ([]*Conversation, error) {
	query := `SELECT id, user_id, business_id, business_name, title, created_at, updated_at, review_context, search_category, search_city, search_state, search_description FROM conversations WHERE user_id = ? ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var convs []*Conversation
	for rows.Next() {
		c := &Conversation{}
		var contextStr sql.NullString
		var category, city, state, description, businessName sql.NullString
		if err := rows.Scan(&c.ID, &c.UserID, &c.BusinessID, &businessName, &c.Title, &c.CreatedAt, &c.UpdatedAt, &contextStr, &category, &city, &state, &description); err != nil {
			return nil, err
		}
		c.BusinessName = businessName.String
		c.SearchCategory = category.String
		c.SearchCity = city.String
		c.SearchState = state.String
		c.SearchDescription = description.String
		if contextStr.Valid && contextStr.String != "" && contextStr.String != "null" {
			if err := json.Unmarshal([]byte(contextStr.String), &c.ReviewContext); err != nil {
				return nil, fmt.Errorf("failed to unmarshal review context: %w", err)
			}
		}
		convs = append(convs, c)
	}
	return convs, nil
}

// GetConversation retrieves a conversation by ID.
func (s *SQLiteStore) GetConversation(ctx context.Context, id string) (*Conversation, error) {
	c := &Conversation{}
	var contextStr sql.NullString
	query := `SELECT id, user_id, business_id, business_name, title, created_at, updated_at, review_context, search_category, search_city, search_state, search_description FROM conversations WHERE id = ?`
	row := s.db.QueryRowContext(ctx, query, id)
	var category, city, state, description, businessName sql.NullString
	err := row.Scan(&c.ID, &c.UserID, &c.BusinessID, &businessName, &c.Title, &c.CreatedAt, &c.UpdatedAt, &contextStr, &category, &city, &state, &description)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.BusinessName = businessName.String
	c.SearchCategory = category.String
	c.SearchCity = city.String
	c.SearchState = state.String
	c.SearchDescription = description.String
	if contextStr.Valid && contextStr.String != "" && contextStr.String != "null" {
		if err := json.Unmarshal([]byte(contextStr.String), &c.ReviewContext); err != nil {
			return nil, fmt.Errorf("failed to unmarshal review context: %w", err)
		}
	}
	return c, nil
}

// DeleteConversation removes a conversation.
func (s *SQLiteStore) DeleteConversation(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM conversations WHERE id = ?", id)
	return err
}

// AddMessage inserts a new message and updates conversation updated_at.
func (s *SQLiteStore) AddMessage(ctx context.Context, msg *Message) error {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	query := `INSERT INTO messages (id, conversation_id, role, content, sequence, created_at) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query, msg.ID, msg.ConversationID, msg.Role, msg.Content, msg.Sequence, msg.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to add message: %w", err)
	}

	// Update conversation's updated_at.
	_, err = s.db.ExecContext(ctx, "UPDATE conversations SET updated_at = ? WHERE id = ?", msg.CreatedAt, msg.ConversationID)
	return err
}

// GetMessages retrieves history for a conversation.
func (s *SQLiteStore) GetMessages(ctx context.Context, conversationID string) ([]*Message, error) {
	query := `SELECT id, conversation_id, role, content, sequence, created_at FROM messages WHERE conversation_id = ? ORDER BY sequence ASC`
	rows, err := s.db.QueryContext(ctx, query, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []*Message
	for rows.Next() {
		m := &Message{}
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.Sequence, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
