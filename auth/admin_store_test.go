package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresStore_SetUserRole(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectExec("UPDATE users SET role").
		WithArgs("admin", "u1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := store.SetUserRole(context.Background(), "u1", "admin")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_SetUserRole_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectExec("UPDATE users SET role").
		WithArgs("admin", "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.SetUserRole(context.Background(), "missing", "admin")
	assert.Error(t, err)
	assert.Equal(t, "user not found", err.Error())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_SetUserRole_Error(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectExec("UPDATE users SET role").
		WithArgs("admin", "u1").
		WillReturnError(errors.New("db down"))

	err := store.SetUserRole(context.Background(), "u1", "admin")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_ListUsers(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	now := time.Now()
	filter := UserFilter{Search: "test", Status: "active"}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM users").
		WithArgs(filter.Search, filter.Status).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	mock.ExpectQuery("SELECT id, email, role, status, created_at FROM users").
		WithArgs(filter.Search, filter.Status, 10, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "role", "status", "created_at"}).
			AddRow("u1", "a@example.com", "user", "active", now).
			AddRow("u2", "b@example.com", "admin", "active", now))

	users, total, err := store.ListUsers(context.Background(), 0, 10, filter)
	assert.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, users, 2)
	assert.Equal(t, "u1", users[0].ID)
	assert.Equal(t, "admin", users[1].Role)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_ListUsers_CountError(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	filter := UserFilter{}
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM users").
		WithArgs(filter.Search, filter.Status).
		WillReturnError(errors.New("db down"))

	users, total, err := store.ListUsers(context.Background(), 0, 10, filter)
	assert.Error(t, err)
	assert.Nil(t, users)
	assert.Equal(t, 0, total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UserDetail(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	now := time.Now()
	profile := []byte(`{"preferences":["vegan"]}`)

	mock.ExpectQuery("FROM users u").
		WithArgs("u1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "role", "status", "created_at", "conversation_count", "message_count", "profile"}).
			AddRow("u1", "a@example.com", "user", "active", now, 3, 7, profile))

	detail, err := store.UserDetail(context.Background(), "u1")
	assert.NoError(t, err)
	require.NotNil(t, detail)
	assert.Equal(t, "u1", detail.User.ID)
	assert.Equal(t, 3, detail.Conversations)
	assert.Equal(t, 7, detail.Messages)
	assert.Equal(t, profile, detail.DietaryProfile)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UserDetail_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectQuery("FROM users u").
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	detail, err := store.UserDetail(context.Background(), "missing")
	assert.NoError(t, err)
	assert.Nil(t, detail)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_DeleteUserPermanently(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectExec("DELETE FROM users WHERE id").
		WithArgs("u1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := store.DeleteUserPermanently(context.Background(), "u1")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_DeleteUserPermanently_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectExec("DELETE FROM users WHERE id").
		WithArgs("missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.DeleteUserPermanently(context.Background(), "missing")
	assert.Error(t, err)
	assert.Equal(t, "user not found", err.Error())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_ResetUserPassword(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectExec("UPDATE users SET password").
		WithArgs("newhash", "u1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := store.ResetUserPassword(context.Background(), "u1", "newhash")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_ResetUserPassword_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectExec("UPDATE users SET password").
		WithArgs("newhash", "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.ResetUserPassword(context.Background(), "missing", "newhash")
	assert.Error(t, err)
	assert.Equal(t, "user not found", err.Error())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_ListAllConversations(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	now := time.Now()
	search := "pizza"

	mock.ExpectQuery("SELECT COUNT\\(\\*\\)\\s+FROM conversations c").
		WithArgs(search).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	mock.ExpectQuery("FROM conversations c").
		WithArgs(search, 10, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "email", "title", "business_id", "business_name", "message_count", "created_at", "updated_at"}).
			AddRow("c1", "u1", "a@example.com", "Title", "550e8400-e29b-41d4-a716-446655440000", "Biz", 5, now, now))

	summaries, total, err := store.ListAllConversations(context.Background(), 0, 10, search)
	assert.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, summaries, 1)
	assert.Equal(t, "c1", summaries[0].ID)
	assert.Equal(t, "a@example.com", summaries[0].UserEmail)
	assert.Equal(t, 5, summaries[0].MessageCount)
	assert.Equal(t, now.Format(time.RFC3339), summaries[0].CreatedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_ListAllConversations_CountError(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\)\\s+FROM conversations c").
		WithArgs("").
		WillReturnError(errors.New("db down"))

	summaries, total, err := store.ListAllConversations(context.Background(), 0, 10, "")
	assert.Error(t, err)
	assert.Nil(t, summaries)
	assert.Equal(t, 0, total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UserAnalytics(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	now := time.Now()

	mock.ExpectQuery("FROM users\\s+WHERE status != 'deleted'").
		WillReturnRows(sqlmock.NewRows([]string{"total", "active", "suspended"}).AddRow(10, 8, 2))

	mock.ExpectQuery("SELECT id, email, role, status, created_at\\s+FROM users").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "role", "status", "created_at"}).
			AddRow("u1", "a@example.com", "user", "active", now))

	analytics, err := store.UserAnalytics(context.Background())
	assert.NoError(t, err)
	require.NotNil(t, analytics)
	assert.Equal(t, 10, analytics.TotalUsers)
	assert.Equal(t, 8, analytics.ActiveUsers)
	assert.Equal(t, 2, analytics.SuspendedUsers)
	require.Len(t, analytics.RecentSignups, 1)
	assert.Equal(t, "u1", analytics.RecentSignups[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UserAnalytics_Error(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectQuery("FROM users\\s+WHERE status != 'deleted'").
		WillReturnError(errors.New("db down"))

	analytics, err := store.UserAnalytics(context.Background())
	assert.Error(t, err)
	assert.Nil(t, analytics)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_ConversationActivity(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectQuery("FROM conversations").
		WithArgs(7).
		WillReturnRows(sqlmock.NewRows([]string{"day", "count"}).
			AddRow("2026-06-12", 3).
			AddRow("2026-06-13", 5))

	activity, err := store.ConversationActivity(context.Background(), 7)
	assert.NoError(t, err)
	require.Len(t, activity, 2)
	assert.Equal(t, "2026-06-12", activity[0].Date)
	assert.Equal(t, 3, activity[0].Count)
	assert.Equal(t, 5, activity[1].Count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_ConversationActivity_Error(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectQuery("FROM conversations").
		WithArgs(7).
		WillReturnError(errors.New("db down"))

	activity, err := store.ConversationActivity(context.Background(), 7)
	assert.Error(t, err)
	assert.Nil(t, activity)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_ConversationAnalytics(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectQuery("FROM conversations").
		WillReturnRows(sqlmock.NewRows([]string{"total", "avg"}).AddRow(20, 2.5))

	analytics, err := store.ConversationAnalytics(context.Background())
	assert.NoError(t, err)
	require.NotNil(t, analytics)
	assert.Equal(t, 20, analytics.TotalConversations)
	assert.Equal(t, 2.5, analytics.AvgPerUser)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_ConversationAnalytics_Error(t *testing.T) {
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectQuery("FROM conversations").
		WillReturnError(errors.New("db down"))

	analytics, err := store.ConversationAnalytics(context.Background())
	assert.Error(t, err)
	assert.Nil(t, analytics)
	assert.NoError(t, mock.ExpectationsWereMet())
}
