package auth

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
)

//go:embed sql/set_user_role.sql
var setUserRoleSQL string

//go:embed sql/list_users.sql
var listUsersSQL string

//go:embed sql/count_users.sql
var countUsersSQL string

//go:embed sql/get_user_detail.sql
var getUserDetailSQL string

//go:embed sql/delete_user_permanently.sql
var deleteUserPermanentlySQL string

//go:embed sql/reset_user_password.sql
var resetUserPasswordSQL string

//go:embed sql/list_all_conversations.sql
var listAllConversationsSQL string

//go:embed sql/count_all_conversations.sql
var countAllConversationsSQL string

//go:embed sql/get_user_analytics.sql
var getUserAnalyticsSQL string

//go:embed sql/get_recent_signups.sql
var getRecentSignupsSQL string

//go:embed sql/get_conversation_activity.sql
var getConversationActivitySQL string

//go:embed sql/get_conversation_analytics.sql
var getConversationAnalyticsSQL string

// PostgresStore implements Store for PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgresStore. Schema creation is handled by
// the centralised migration runner (internal/db); this constructor only opens
// the database connection.
func NewPostgresStore(ctx context.Context, dataSourceName string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("failed to open PostgreSQL database: %w", err)
	}

	err = db.PingContext(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	return &PostgresStore{db: db}, nil
}

// CreateUser inserts a new user into the database.
func (s *PostgresStore) CreateUser(ctx context.Context, user *User) error {
	if user.Status == "" {
		user.Status = "active"
	}
	if user.Role == "" {
		user.Role = "user"
	}
	query := `INSERT INTO users (id, email, password, role, status, created_at) VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := s.db.ExecContext(ctx, query, user.ID, user.Email, user.Password, user.Role, user.Status, user.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

// DietaryProfile retrieves a user's dietary profile.
func (s *PostgresStore) DietaryProfile(ctx context.Context, userID string) ([]byte, error) {
	var profile []byte
	query := `SELECT profile FROM user_profiles WHERE user_id = $1`
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&profile)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // Profile not found, return nil without error
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get dietary profile: %w", err)
	}
	return profile, nil
}

// SaveDietaryProfile upserts a user's dietary profile.
func (s *PostgresStore) SaveDietaryProfile(ctx context.Context, userID string, profile []byte) error {
	query := `INSERT INTO user_profiles (user_id, profile) VALUES ($1, $2) ON CONFLICT (user_id) DO UPDATE SET profile = EXCLUDED.profile, updated_at = CURRENT_TIMESTAMP`
	_, err := s.db.ExecContext(ctx, query, userID, profile)
	if err != nil {
		return fmt.Errorf("failed to save dietary profile: %w", err)
	}
	return nil
}

// UserByEmail retrieves a user by their email address.
func (s *PostgresStore) UserByEmail(ctx context.Context, email string) (*User, error) {
	user := &User{}
	query := `SELECT id, email, password, role, status, created_at FROM users WHERE email = $1`
	row := s.db.QueryRowContext(ctx, query, email)
	err := row.Scan(&user.ID, &user.Email, &user.Password, &user.Role, &user.Status, &user.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // User not found
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user by email: %w", err)
	}
	return user, nil
}

// UserByID retrieves a user by their ID.
func (s *PostgresStore) UserByID(ctx context.Context, id string) (*User, error) {
	user := &User{}
	query := `SELECT id, email, password, role, status, created_at FROM users WHERE id = $1`
	row := s.db.QueryRowContext(ctx, query, id)
	err := row.Scan(&user.ID, &user.Email, &user.Password, &user.Role, &user.Status, &user.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // User not found
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user by ID: %w", err)
	}
	return user, nil
}

// UpdateUserStatus updates a user's status (e.g. "active", "deleted").
func (s *PostgresStore) UpdateUserStatus(ctx context.Context, userID string, status string) error {
	result, err := s.db.ExecContext(ctx, "UPDATE users SET status = $1 WHERE id = $2", status, userID)
	if err != nil {
		return fmt.Errorf("failed to update user status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// CreateConversation inserts a new conversation.
func (s *PostgresStore) CreateConversation(ctx context.Context, conv *Conversation) error {
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

	query := `INSERT INTO conversations (id, user_id, business_id, business_name, title, created_at, updated_at, review_context, search_category, search_city, search_state, search_description) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
	_, err = s.db.ExecContext(ctx, query, conv.ID, conv.UserID, conv.BusinessID, conv.BusinessName, conv.Title, conv.CreatedAt, conv.UpdatedAt, string(contextJSON), conv.SearchCategory, conv.SearchCity, conv.SearchState, conv.SearchDescription)
	if err != nil {
		return fmt.Errorf("failed to create conversation: %w", err)
	}
	return nil
}

// ListConversations returns all conversations for a user.
func (s *PostgresStore) ListConversations(ctx context.Context, userID string) ([]*Conversation, error) {
	query := `SELECT id, user_id, business_id, business_name, title, created_at, updated_at, review_context, search_category, search_city, search_state, search_description FROM conversations WHERE user_id = $1 ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

// Conversation retrieves a conversation by ID.
func (s *PostgresStore) Conversation(ctx context.Context, id string) (*Conversation, error) {
	c := &Conversation{}
	var contextStr sql.NullString
	query := `SELECT id, user_id, business_id, business_name, title, created_at, updated_at, review_context, search_category, search_city, search_state, search_description FROM conversations WHERE id = $1`
	row := s.db.QueryRowContext(ctx, query, id)
	var category, city, state, description, businessName sql.NullString
	err := row.Scan(&c.ID, &c.UserID, &c.BusinessID, &businessName, &c.Title, &c.CreatedAt, &c.UpdatedAt, &contextStr, &category, &city, &state, &description)
	if errors.Is(err, sql.ErrNoRows) {
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
func (s *PostgresStore) DeleteConversation(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM conversations WHERE id = $1", id)
	return err
}

// AddMessage inserts a new message and updates conversation updated_at.
func (s *PostgresStore) AddMessage(ctx context.Context, msg *Message) error {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	query := `INSERT INTO messages (id, conversation_id, role, content, sequence, created_at) VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := s.db.ExecContext(ctx, query, msg.ID, msg.ConversationID, msg.Role, msg.Content, msg.Sequence, msg.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to add message: %w", err)
	}

	// Update conversation's updated_at.
	_, err = s.db.ExecContext(ctx, "UPDATE conversations SET updated_at = $1 WHERE id = $2", msg.CreatedAt, msg.ConversationID)
	return err
}

// Messages retrieves history for a conversation.
func (s *PostgresStore) Messages(ctx context.Context, conversationID string) ([]*Message, error) {
	query := `SELECT id, conversation_id, role, content, sequence, created_at FROM messages WHERE conversation_id = $1 ORDER BY sequence ASC`
	rows, err := s.db.QueryContext(ctx, query, conversationID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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
func (s *PostgresStore) Close() error {
	return s.db.Close()
}

// SetUserRole sets a user's role.
func (s *PostgresStore) SetUserRole(ctx context.Context, userID string, role string) error {
	result, err := s.db.ExecContext(ctx, setUserRoleSQL, role, userID)
	if err != nil {
		return fmt.Errorf("failed to set user role: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// ListUsers queries users filtering by email and status.
func (s *PostgresStore) ListUsers(ctx context.Context, offset, limit int, filter UserFilter) ([]*User, int, error) {
	var total int
	err := s.db.QueryRowContext(ctx, countUsersSQL, filter.Search, filter.Status).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count users: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, listUsersSQL, filter.Search, filter.Status, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.Status, &u.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, total, nil
}

// UserDetail returns comprehensive statistics and status about a user.
func (s *PostgresStore) UserDetail(ctx context.Context, userID string) (*UserDetail, error) {
	var u User
	var conversations int
	var messages int
	var profile []byte

	err := s.db.QueryRowContext(ctx, getUserDetailSQL, userID).Scan(
		&u.ID, &u.Email, &u.Role, &u.Status, &u.CreatedAt,
		&conversations, &messages, &profile,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // User not found
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query user detail: %w", err)
	}

	return &UserDetail{
		User:           &u,
		Conversations:  conversations,
		Messages:       messages,
		DietaryProfile: profile,
	}, nil
}

// DeleteUserPermanently hard deletes a user and cascades deletion to other entities.
func (s *PostgresStore) DeleteUserPermanently(ctx context.Context, userID string) error {
	result, err := s.db.ExecContext(ctx, deleteUserPermanentlySQL, userID)
	if err != nil {
		return fmt.Errorf("failed to permanently delete user: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// ResetUserPassword updates a user's password.
func (s *PostgresStore) ResetUserPassword(ctx context.Context, userID string, hashedPassword string) error {
	result, err := s.db.ExecContext(ctx, resetUserPasswordSQL, hashedPassword, userID)
	if err != nil {
		return fmt.Errorf("failed to reset user password: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// ListAllConversations returns all conversations with email details and counts.
func (s *PostgresStore) ListAllConversations(ctx context.Context, offset, limit int, search string) ([]*ConversationSummary, int, error) {
	var total int
	err := s.db.QueryRowContext(ctx, countAllConversationsSQL, search).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count conversations: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, listAllConversationsSQL, search, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query conversations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []*ConversationSummary
	for rows.Next() {
		c := &ConversationSummary{}
		var created, updated time.Time
		if err := rows.Scan(
			&c.ID, &c.UserID, &c.UserEmail, &c.Title, &c.BusinessID, &c.BusinessName,
			&c.MessageCount, &created, &updated,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan conversation summary: %w", err)
		}
		c.CreatedAt = created.Format(time.RFC3339)
		c.UpdatedAt = updated.Format(time.RFC3339)
		summaries = append(summaries, c)
	}
	return summaries, total, nil
}

// UserAnalytics returns counts of users.
func (s *PostgresStore) UserAnalytics(ctx context.Context) (*UserAnalytics, error) {
	var total, active, suspended int
	err := s.db.QueryRowContext(ctx, getUserAnalyticsSQL).Scan(&total, &active, &suspended)
	if err != nil {
		return nil, fmt.Errorf("failed to query user analytics: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, getRecentSignupsSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent signups: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var recent []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.Status, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan recent signup: %w", err)
		}
		recent = append(recent, u)
	}

	return &UserAnalytics{
		TotalUsers:     total,
		ActiveUsers:    active,
		SuspendedUsers: suspended,
		RecentSignups:  recent,
	}, nil
}

// ConversationActivity returns day-by-day counts.
func (s *PostgresStore) ConversationActivity(ctx context.Context, days int) ([]DailyCount, error) {
	rows, err := s.db.QueryContext(ctx, getConversationActivitySQL, days)
	if err != nil {
		return nil, fmt.Errorf("failed to query conversation activity: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var activity []DailyCount
	for rows.Next() {
		var dc DailyCount
		if err := rows.Scan(&dc.Date, &dc.Count); err != nil {
			return nil, fmt.Errorf("failed to scan daily count: %w", err)
		}
		activity = append(activity, dc)
	}
	return activity, nil
}

// ConversationAnalytics returns total and average conversations.
func (s *PostgresStore) ConversationAnalytics(ctx context.Context) (*ConversationAnalytics, error) {
	var total int
	var avg float64
	err := s.db.QueryRowContext(ctx, getConversationAnalyticsSQL).Scan(&total, &avg)
	if err != nil {
		return nil, fmt.Errorf("failed to query conversation analytics: %w", err)
	}

	return &ConversationAnalytics{
		TotalConversations: total,
		AvgPerUser:         avg,
	}, nil
}
