package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"fodmap/auth"
)

type mockUserStore struct {
	users         map[string]*auth.User
	conversations map[string]*auth.Conversation
	messages      map[string][]*auth.Message
	profiles      map[string][]byte
}

func newMockStore() *mockUserStore {
	return &mockUserStore{
		users:         make(map[string]*auth.User),
		conversations: make(map[string]*auth.Conversation),
		messages:      make(map[string][]*auth.Message),
		profiles:      make(map[string][]byte),
	}
}

func (m *mockUserStore) CreateUser(ctx context.Context, user *auth.User) error {
	if _, ok := m.users[user.Email]; ok {
		return fmt.Errorf("user already exists")
	}
	if user.Status == "" {
		user.Status = "active"
	}
	if user.Role == "" {
		user.Role = "user"
	}
	m.users[user.Email] = user
	return nil
}

func (m *mockUserStore) GetDietaryProfile(ctx context.Context, userID string) ([]byte, error) {
	if profile, ok := m.profiles[userID]; ok {
		return profile, nil
	}
	return nil, nil
}

func (m *mockUserStore) SaveDietaryProfile(ctx context.Context, userID string, profile []byte) error {
	m.profiles[userID] = profile
	return nil
}

func (m *mockUserStore) GetUserByEmail(ctx context.Context, email string) (*auth.User, error) {
	user, ok := m.users[email]
	if !ok {
		return nil, nil
	}
	return user, nil
}

func (m *mockUserStore) GetUserByID(ctx context.Context, id string) (*auth.User, error) {
	for _, u := range m.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, nil
}

func (m *mockUserStore) UpdateUserStatus(ctx context.Context, userID string, status string) error {
	for _, u := range m.users {
		if u.ID == userID {
			u.Status = status
			return nil
		}
	}
	return fmt.Errorf("not found")
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

func (m *mockUserStore) SetUserRole(ctx context.Context, userID string, role string) error {
	for _, u := range m.users {
		if u.ID == userID {
			u.Role = role
			return nil
		}
	}
	return fmt.Errorf("user not found")
}

func (m *mockUserStore) ListUsers(ctx context.Context, offset, limit int, filter auth.UserFilter) ([]*auth.User, int, error) {
	var filtered []*auth.User
	for _, u := range m.users {
		if u.Status == "deleted" {
			continue
		}
		if filter.Search != "" && !strings.Contains(strings.ToLower(u.Email), strings.ToLower(filter.Search)) {
			continue
		}
		if filter.Status != "" && u.Status != filter.Status {
			continue
		}
		filtered = append(filtered, u)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].Email > filtered[j].Email
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})

	total := len(filtered)
	if offset > total {
		return []*auth.User{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return filtered[offset:end], total, nil
}

func (m *mockUserStore) GetUserDetail(ctx context.Context, userID string) (*auth.UserDetail, error) {
	var user *auth.User
	for _, u := range m.users {
		if u.ID == userID {
			if u.Status == "deleted" {
				return nil, nil
			}
			user = u
			break
		}
	}
	if user == nil {
		return nil, nil
	}

	convCount := 0
	msgCount := 0
	for _, c := range m.conversations {
		if c.UserID == userID {
			convCount++
			msgCount += len(m.messages[c.ID])
		}
	}

	profile := m.profiles[userID]

	return &auth.UserDetail{
		User:           user,
		Conversations:  convCount,
		Messages:       msgCount,
		DietaryProfile: profile,
	}, nil
}

func (m *mockUserStore) DeleteUserPermanently(ctx context.Context, userID string) error {
	var targetUser *auth.User
	var targetEmail string
	for email, u := range m.users {
		if u.ID == userID {
			targetUser = u
			targetEmail = email
			break
		}
	}
	if targetUser == nil {
		return fmt.Errorf("user not found")
	}

	delete(m.users, targetEmail)
	delete(m.profiles, userID)

	// Cascade delete conversations and messages
	for cid, c := range m.conversations {
		if c.UserID == userID {
			delete(m.conversations, cid)
			delete(m.messages, cid)
		}
	}
	return nil
}

func (m *mockUserStore) ResetUserPassword(ctx context.Context, userID string, hashedPassword string) error {
	for _, u := range m.users {
		if u.ID == userID {
			u.Password = hashedPassword
			return nil
		}
	}
	return fmt.Errorf("user not found")
}

func (m *mockUserStore) ListAllConversations(ctx context.Context, offset, limit int, search string) ([]*auth.ConversationSummary, int, error) {
	var filtered []*auth.Conversation
	for _, c := range m.conversations {
		user, err := m.GetUserByID(ctx, c.UserID)
		userEmail := ""
		if err == nil && user != nil {
			userEmail = user.Email
		}
		if search != "" {
			sLower := strings.ToLower(search)
			titleMatch := strings.Contains(strings.ToLower(c.Title), sLower)
			emailMatch := strings.Contains(strings.ToLower(userEmail), sLower)
			if !titleMatch && !emailMatch {
				continue
			}
		}
		filtered = append(filtered, c)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
	})

	total := len(filtered)
	var summaries []*auth.ConversationSummary

	start := offset
	if start > total {
		start = total
	}
	end := offset + limit
	if end > total {
		end = total
	}

	for _, c := range filtered[start:end] {
		user, _ := m.GetUserByID(ctx, c.UserID)
		email := ""
		if user != nil {
			email = user.Email
		}
		summaries = append(summaries, &auth.ConversationSummary{
			ID:           c.ID,
			UserID:       c.UserID,
			UserEmail:    email,
			Title:        c.Title,
			BusinessID:   c.BusinessID,
			BusinessName: c.BusinessName,
			MessageCount: len(m.messages[c.ID]),
			CreatedAt:    c.CreatedAt.Format(time.RFC3339),
			UpdatedAt:    c.UpdatedAt.Format(time.RFC3339),
		})
	}

	return summaries, total, nil
}

func (m *mockUserStore) GetUserAnalytics(ctx context.Context) (*auth.UserAnalytics, error) {
	total := 0
	active := 0
	suspended := 0
	var activeUsers []*auth.User

	for _, u := range m.users {
		if u.Status != "deleted" {
			total++
			switch u.Status {
			case "active":
				active++
			case "suspended":
				suspended++
			}
			activeUsers = append(activeUsers, u)
		}
	}

	sort.Slice(activeUsers, func(i, j int) bool {
		return activeUsers[i].CreatedAt.After(activeUsers[j].CreatedAt)
	})

	recentLimit := 5
	if len(activeUsers) < recentLimit {
		recentLimit = len(activeUsers)
	}

	return &auth.UserAnalytics{
		TotalUsers:     total,
		ActiveUsers:    active,
		SuspendedUsers: suspended,
		RecentSignups:  activeUsers[:recentLimit],
	}, nil
}

func (m *mockUserStore) GetConversationActivity(ctx context.Context, days int) ([]auth.DailyCount, error) {
	cutoff := time.Now().AddDate(0, 0, -days)
	counts := make(map[string]int)

	for _, c := range m.conversations {
		if c.CreatedAt.After(cutoff) {
			dayStr := c.CreatedAt.Format("2006-01-02")
			counts[dayStr]++
		}
	}

	var activity []auth.DailyCount
	for day, count := range counts {
		activity = append(activity, auth.DailyCount{
			Date:  day,
			Count: count,
		})
	}

	sort.Slice(activity, func(i, j int) bool {
		return activity[i].Date < activity[j].Date
	})

	return activity, nil
}

func (m *mockUserStore) GetConversationAnalytics(ctx context.Context) (*auth.ConversationAnalytics, error) {
	totalConvs := len(m.conversations)
	userCount := 0
	for _, u := range m.users {
		if u.Status != "deleted" {
			userCount++
		}
	}

	avg := 0.0
	if userCount > 0 {
		avg = float64(totalConvs) / float64(userCount)
	}

	return &auth.ConversationAnalytics{
		TotalConversations: totalConvs,
		AvgPerUser:         avg,
	}, nil
}
