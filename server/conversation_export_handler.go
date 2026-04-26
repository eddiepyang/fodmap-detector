package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"fodmap/auth"
)

// exportConversationHandler returns a conversation transcript in JSON or
// Markdown format. The format is controlled by the "format" query parameter
// (default: "json").
func (s *Server) exportConversationHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userContextKey).(string)
	if !ok || userID == "" {
		respondError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	convID := r.PathValue("id")
	conv, err := s.userStore.GetConversation(r.Context(), convID)
	if err != nil {
		respondError(w, "failed to load conversation", http.StatusInternalServerError)
		return
	}
	if conv == nil {
		respondError(w, "conversation not found", http.StatusNotFound)
		return
	}
	if conv.UserID != userID {
		respondError(w, "forbidden", http.StatusForbidden)
		return
	}

	messages, err := s.userStore.GetMessages(r.Context(), convID)
	if err != nil {
		respondError(w, "failed to load messages", http.StatusInternalServerError)
		return
	}

	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "json"
	}

	switch format {
	case "markdown", "md":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=conversation-%s.md", convID[:8]))
		writeConversationMarkdown(w, conv, messages)
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=conversation-%s.json", convID[:8]))
		writeConversationJSON(w, conv, messages)
	default:
		respondError(w, "unsupported format; use 'json' or 'markdown'", http.StatusBadRequest)
	}
}

// writeConversationJSON writes a full structured export as JSON.
func writeConversationJSON(w http.ResponseWriter, conv *auth.Conversation, messages []*auth.Message) {
	type export struct {
		Conversation *auth.Conversation `json:"conversation"`
		Messages     []*auth.Message    `json:"messages"`
		ExportedAt   time.Time          `json:"exported_at"`
	}
	_ = json.NewEncoder(w).Encode(export{
		Conversation: conv,
		Messages:     messages,
		ExportedAt:   time.Now(),
	})
}

// capitalize uppercases the first character of s. It replaces the deprecated
// strings.Title for the simple single-word role names used in message export.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// writeConversationMarkdown writes a human-readable transcript in Markdown.
func writeConversationMarkdown(w http.ResponseWriter, conv *auth.Conversation, messages []*auth.Message) {
	_, _ = fmt.Fprintf(w, "# %s\n\n", conv.Title)
	_, _ = fmt.Fprintf(w, "- **Conversation ID:** %s\n", conv.ID)
	_, _ = fmt.Fprintf(w, "- **Business:** %s (%s, %s)\n", conv.BusinessName, conv.SearchCity, conv.SearchState)
	_, _ = fmt.Fprintf(w, "- **Created:** %s\n", conv.CreatedAt.Format(time.RFC3339))
	_, _ = fmt.Fprintf(w, "- **Exported:** %s\n\n", time.Now().Format(time.RFC3339))
	_, _ = fmt.Fprintf(w, "---\n\n")

	for _, m := range messages {
		switch m.Role {
		case "user":
			_, _ = fmt.Fprintf(w, "### 👤 User\n\n%s\n\n", m.Content)
		case "model":
			_, _ = fmt.Fprintf(w, "### 🤖 Assistant\n\n%s\n\n", m.Content)
		case "tool_call":
			_, _ = fmt.Fprintf(w, "### 🔧 Tool Call\n\n```json\n%s\n```\n\n", m.Content)
		case "tool_response":
			_, _ = fmt.Fprintf(w, "### 📋 Tool Response\n\n```json\n%s\n```\n\n", m.Content)
		default:
			_, _ = fmt.Fprintf(w, "### %s\n\n%s\n\n", capitalize(m.Role), m.Content)
		}
	}
}
