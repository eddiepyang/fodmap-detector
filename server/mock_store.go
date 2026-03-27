package server

import (
	"context"
	"fmt"

	"fodmap/auth"
)

type mockUserStore struct {
	users         map[string]*auth.User
	conversations map[string]*auth.Conversation
	messages      map[string][]*auth.Message
}

func newMockStore() *mockUserStore {
	return &mockUserStore{
		users:         make(map[string]*auth.User),
		conversations: make(map[string]*auth.Conversation),
		messages:      make(map[string][]*auth.Message),
	}
}

func (m *mockUserStore) CreateUser(ctx context.Context, user *auth.User) error {
	if _, ok := m.users[user.Email]; ok {
		return fmt.Errorf("user already exists")
	}
	m.users[user.Email] = user
	return nil
}

func (m *mockUserStore) GetUserByEmail(ctx context.Context, email string) (*auth.User, error) {
	user, ok := m.users[email]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return user, nil
}

func (m *mockUserStore) GetUserByID(ctx context.Context, id string) (*auth.User, error) {
	for _, u := range m.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockUserStore) CreateConversation(ctx context.Context, conv *auth.Conversation) error {
	m.conversations[conv.ID] = conv
	return nil
}

func (m *mockUserStore) ListConversations(ctx context.Context, userID string) ([]*auth.Conversation, error) {
	var results []*auth.Conversation
	for _, c := range m.conversations {
		if c.UserID == userID {
			results = append(results, c)
		}
	}
	return results, nil
}

func (m *mockUserStore) GetConversation(ctx context.Context, id string) (*auth.Conversation, error) {
	c, ok := m.conversations[id]
	if !ok {
		return nil, nil
	}
	return c, nil
}

func (m *mockUserStore) DeleteConversation(ctx context.Context, id string) error {
	delete(m.conversations, id)
	delete(m.messages, id)
	return nil
}

func (m *mockUserStore) AddMessage(ctx context.Context, msg *auth.Message) error {
	m.messages[msg.ConversationID] = append(m.messages[msg.ConversationID], msg)
	return nil
}

func (m *mockUserStore) GetMessages(ctx context.Context, conversationID string) ([]*auth.Message, error) {
	return m.messages[conversationID], nil
}

func (m *mockUserStore) Close() error { return nil }
