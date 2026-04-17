package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	return &PostgresStore{db: db}, mock
}

func TestPostgresStore_CreateUser(t *testing.T) {
	store, mock := newMockStore(t)
	defer store.Close()

	user := &User{
		ID:        "u1",
		Email:     "test@example.com",
		Password:  "hash",
		CreatedAt: time.Now(),
	}

	mock.ExpectExec("INSERT INTO users").
		WithArgs(user.ID, user.Email, user.Password, user.CreatedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := store.CreateUser(context.Background(), user)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_GetUserByEmail(t *testing.T) {
	store, mock := newMockStore(t)
	defer store.Close()

	email := "test@example.com"
	now := time.Now()

	mock.ExpectQuery("SELECT id, email, password, created_at FROM users WHERE email = \\$1").
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "password", "created_at"}).
			AddRow("u1", email, "hash", now))

	user, err := store.GetUserByEmail(context.Background(), email)
	assert.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, "u1", user.ID)
	assert.Equal(t, email, user.Email)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_GetUserByEmail_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	defer store.Close()

	mock.ExpectQuery("SELECT id, email, password, created_at FROM users").
		WithArgs("missing@example.com").
		WillReturnError(sql.ErrNoRows)

	user, err := store.GetUserByEmail(context.Background(), "missing@example.com")
	assert.NoError(t, err)
	assert.Nil(t, user)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_GetUserByID(t *testing.T) {
	store, mock := newMockStore(t)
	defer store.Close()

	id := "u1"
	now := time.Now()

	mock.ExpectQuery("SELECT id, email, password, created_at FROM users WHERE id = \\$1").
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "password", "created_at"}).
			AddRow(id, "test@example.com", "hash", now))

	user, err := store.GetUserByID(context.Background(), id)
	assert.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, id, user.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_GetUserByID_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	defer store.Close()

	mock.ExpectQuery("SELECT id, email, password, created_at FROM users").
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	user, err := store.GetUserByID(context.Background(), "missing")
	assert.NoError(t, err)
	assert.Nil(t, user)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_CreateConversation(t *testing.T) {
	store, mock := newMockStore(t)
	defer store.Close()

	conv := &Conversation{
		ID:                "c1",
		UserID:            "u1",
		BusinessID:        "b1",
		BusinessName:      "Biz",
		Title:             "Test Title",
		SearchCategory:    "pizza",
		SearchCity:        "Austin",
		SearchState:       "TX",
		SearchDescription: "description",
	}

	mock.ExpectExec("INSERT INTO conversations").
		WithArgs(conv.ID, conv.UserID, conv.BusinessID, conv.BusinessName, conv.Title, sqlmock.AnyArg(), sqlmock.AnyArg(), "", conv.SearchCategory, conv.SearchCity, conv.SearchState, conv.SearchDescription).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := store.CreateConversation(context.Background(), conv)
	assert.NoError(t, err)
	assert.False(t, conv.CreatedAt.IsZero())
	assert.False(t, conv.UpdatedAt.IsZero())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_ListConversations(t *testing.T) {
	store, mock := newMockStore(t)
	defer store.Close()

	userID := "u1"
	now := time.Now()

	mock.ExpectQuery("SELECT id, user_id, business_id, business_name, title, created_at, updated_at, review_context, search_category, search_city, search_state, search_description FROM conversations WHERE user_id = \\$1 ORDER BY updated_at DESC").
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "business_id", "business_name", "title", "created_at", "updated_at", "review_context", "search_category", "search_city", "search_state", "search_description"}).
			AddRow("c1", userID, "b1", "Biz1", "Title 1", now, now, "", "p", "a", "t", "d").
			AddRow("c2", userID, "b2", "Biz2", "Title 2", now, now, "null", "", "", "", ""))

	convs, err := store.ListConversations(context.Background(), userID)
	assert.NoError(t, err)
	require.Len(t, convs, 2)
	assert.Equal(t, "c1", convs[0].ID)
	assert.Equal(t, "Biz1", convs[0].BusinessName)
	assert.Equal(t, "c2", convs[1].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_GetConversation(t *testing.T) {
	store, mock := newMockStore(t)
	defer store.Close()

	id := "c1"
	now := time.Now()

	mock.ExpectQuery("SELECT id, user_id, business_id, business_name, title, created_at, updated_at, review_context, search_category, search_city, search_state, search_description FROM conversations WHERE id = \\$1").
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "business_id", "business_name", "title", "created_at", "updated_at", "review_context", "search_category", "search_city", "search_state", "search_description"}).
			AddRow(id, "u1", "b1", "Biz1", "Title", now, now, nil, "p", "a", "t", "d"))

	conv, err := store.GetConversation(context.Background(), id)
	assert.NoError(t, err)
	require.NotNil(t, conv)
	assert.Equal(t, id, conv.ID)
	assert.Equal(t, "Biz1", conv.BusinessName)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_GetConversation_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	defer store.Close()

	mock.ExpectQuery("SELECT id, user_id, business_id, business_name, title, created_at, updated_at, review_context, search_category, search_city, search_state, search_description FROM conversations").
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	conv, err := store.GetConversation(context.Background(), "missing")
	assert.NoError(t, err)
	assert.Nil(t, conv)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_DeleteConversation(t *testing.T) {
	store, mock := newMockStore(t)
	defer store.Close()

	id := "c1"
	mock.ExpectExec("DELETE FROM conversations WHERE id = \\$1").
		WithArgs(id).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := store.DeleteConversation(context.Background(), id)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_AddMessage(t *testing.T) {
	store, mock := newMockStore(t)
	defer store.Close()

	msg := &Message{
		ID:             "m1",
		ConversationID: "c1",
		Role:           "user",
		Content:        "hello",
		Sequence:       1,
	}

	mock.ExpectExec("INSERT INTO messages").
		WithArgs(msg.ID, msg.ConversationID, msg.Role, msg.Content, msg.Sequence, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	
	mock.ExpectExec("UPDATE conversations SET updated_at = \\$1 WHERE id = \\$2").
		WithArgs(sqlmock.AnyArg(), msg.ConversationID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := store.AddMessage(context.Background(), msg)
	assert.NoError(t, err)
	assert.False(t, msg.CreatedAt.IsZero())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_GetMessages(t *testing.T) {
	store, mock := newMockStore(t)
	defer store.Close()

	conversationID := "c1"
	now := time.Now()

	mock.ExpectQuery("SELECT id, conversation_id, role, content, sequence, created_at FROM messages WHERE conversation_id = \\$1 ORDER BY sequence ASC").
		WithArgs(conversationID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "conversation_id", "role", "content", "sequence", "created_at"}).
			AddRow("m1", conversationID, "user", "hello", 1, now).
			AddRow("m2", conversationID, "assistant", "world", 2, now))

	msgs, err := store.GetMessages(context.Background(), conversationID)
	assert.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "m1", msgs[0].ID)
	assert.Equal(t, "m2", msgs[1].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}
