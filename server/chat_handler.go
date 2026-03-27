package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"fodmap/auth"
	"fodmap/chat"
	"fodmap/search"

	"github.com/google/uuid"
	"google.golang.org/genai"
)

const (
	chatRequestTimeout = 30 * time.Second
	maxBodySize        = 8 * 1024 // 8 KB
	defaultChatLimit   = 20
)

// chatRequest is the JSON body for POST /chat/{query...}.
type chatRequest struct {
	Message  string `json:"message"`
	Limit    int    `json:"limit"`
	Category       string `json:"category"`
	City           string `json:"city"`
	State          string `json:"state"`
	ConversationID string `json:"conversation_id"`
}

type chatResponse struct {
	Business  chatBusinessResponse `json:"business"`
	Answer         string               `json:"answer"`
	ToolCalls      []string             `json:"tool_calls"`
	ConversationID string               `json:"conversation_id"`
}

type chatBusinessResponse struct {
	Name  string `json:"name"`
	City  string `json:"city"`
	State string `json:"state"`
}

// GeminiChatFactory creates a Gemini chat session given a system prompt.
// Extracted as a function type so tests can inject stubs.
type GeminiChatFactory func(ctx context.Context, systemPrompt string) (*genai.Client, *genai.Chat, error)

func (s *Server) chatHandler(client *genai.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.PathValue("query")
		if query == "" {
			respondError(w, "search query is required", http.StatusBadRequest)
			return
		}

		// Limit body size.
		r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Message == "" {
			respondError(w, "message is required", http.StatusBadRequest)
			return
		}

		if err := chat.ValidateChatInput(req.Message); err != nil {
			respondError(w, err.Error(), http.StatusBadRequest)
			return
		}

		if s.searcher == nil {
			respondError(w, "search service not configured", http.StatusServiceUnavailable)
			return
		}

		limit := req.Limit
		if limit <= 0 {
			limit = defaultChatLimit
		}

		// Enforce a request-level timeout.
		ctx, cancel := context.WithTimeout(r.Context(), chatRequestTimeout)
		defer cancel()

		// Load existing conversation or create a new one.
		var conv *auth.Conversation
		var history []*genai.Content
		userID, _ := r.Context().Value(userContextKey).(string)

		if req.ConversationID != "" {
			var err error
			conv, err = s.userStore.GetConversation(ctx, req.ConversationID)
			if err != nil || conv == nil {
				respondError(w, "conversation not found", http.StatusNotFound)
				return
			}
			if userID != "" && conv.UserID != userID {
				respondError(w, "forbidden", http.StatusForbidden)
				return
			}

			// Load history.
			dbMessages, err := s.userStore.GetMessages(ctx, conv.ID)
			if err != nil {
				respondError(w, "failed to load history", http.StatusInternalServerError)
				return
			}
			history = messagesToContent(dbMessages)
		}

		var biz chatBusinessResponse
		var systemPrompt string

		if conv == nil {
			// 1. Find the top business (only if starting a new conversation).
			filter := search.SearchFilter{
				Category: req.Category,
				City:     req.City,
				State:    req.State,
			}
			bizResult, err := s.searcher.GetBusinesses(ctx, query, 1, filter)
			if err != nil {
				slog.Error("chat: search businesses", "error", err)
				respondError(w, "business search failed", http.StatusInternalServerError)
				return
			}
			if len(bizResult.Businesses) == 0 {
				respondError(w, "no businesses found for query", http.StatusNotFound)
				return
			}
			b := bizResult.Businesses[0]
			biz = chatBusinessResponse{Name: b.Name, City: b.City, State: b.State}

			// 2. Fetch reviews for that business.
			reviewFilter := search.SearchFilter{BusinessID: b.ID}
			reviewResult, err := s.searcher.GetReviews(ctx, query, limit, reviewFilter)
			if err != nil {
				slog.Error("chat: search reviews", "error", err)
				respondError(w, "review search failed", http.StatusInternalServerError)
				return
			}
			reviews := make([]chat.Review, len(reviewResult.BusinessReviews))
			for i, rr := range reviewResult.BusinessReviews {
				reviews[i] = chat.Review{
					Stars: float32(rr.Score),
					Text:  rr.Review.Review.Text,
				}
			}

			// 3. Render the system prompt.
			chatBiz := &chat.Business{ID: b.ID, Name: b.Name, City: b.City, State: b.State}
			systemPrompt, err = chat.RenderChatSystemPrompt(chat.DefaultChatInstruction, chatBiz, reviews)
			if err != nil {
				slog.Error("chat: render prompt", "error", err)
				respondError(w, "failed to build chat context", http.StatusInternalServerError)
				return
			}

			// 4. Create new conversation in DB.
			convID := uuid.New().String()
			if userID == "" {
				userID = "anonymous"
			}
			conv = &auth.Conversation{
				ID:         convID,
				UserID:     userID,
				BusinessID: b.ID,
				Title:      "Chat about " + b.Name,
			}
			if err := s.userStore.CreateConversation(ctx, conv); err != nil {
				slog.Error("chat: create conversation", "error", err)
				respondError(w, "failed to create conversation", http.StatusInternalServerError)
				return
			}
		} else {
			// Existing conversation: rebuild system prompt from business ID.
			// (Future optimization: cache the system prompt or store it).
			// For now, we search the business again to get details.
			b, err := s.searcher.GetBusinesses(ctx, conv.BusinessID, 1, search.SearchFilter{BusinessID: conv.BusinessID})
			if err != nil || len(b.Businesses) == 0 {
				respondError(w, "failed to reload business context", http.StatusInternalServerError)
				return
			}
			biz = chatBusinessResponse{Name: b.Businesses[0].Name, City: b.Businesses[0].City, State: b.Businesses[0].State}
			
			reviewResult, err := s.searcher.GetReviews(ctx, "", limit, search.SearchFilter{BusinessID: conv.BusinessID})
			if err != nil {
				slog.Error("chat: reload reviews", "error", err)
				respondError(w, "failed to reload review context", http.StatusInternalServerError)
				return
			}
			reviews := make([]chat.Review, len(reviewResult.BusinessReviews))
			for i, rr := range reviewResult.BusinessReviews {
				reviews[i] = chat.Review{Stars: float32(rr.Score), Text: rr.Review.Review.Text}
			}
			chatBiz := &chat.Business{ID: conv.BusinessID, Name: biz.Name, City: biz.City, State: biz.State}
			systemPrompt, err = chat.RenderChatSystemPrompt(chat.DefaultChatInstruction, chatBiz, reviews)
			if err != nil {
				slog.Error("chat: render prompt on resume", "error", err)
				respondError(w, "failed to build chat context", http.StatusInternalServerError)
				return
			}
		}

		chatModel := s.chatModel
		if chatModel == "" {
			chatModel = "gemini-3-flash-preview"
		}

		if client == nil {
			respondError(w, "chat service not configured", http.StatusServiceUnavailable)
			return
		}

		// Topic pre-screen.
		if foodRelated, err := chat.IsFoodRelated(ctx, client, s.filterModel, req.Message); err != nil {
			slog.Warn("chat: topic screen error", "error", err)
		} else if !foodRelated {
			respondError(w, "I can only help with food, ingredients, FODMAP, and allergen questions", http.StatusBadRequest)
			return
		}

		// Build a session using the server's own searcher for FODMAP lookups.
		fodmapClient := NewDirectFodmapClient(s)
		allergenClient := chat.NewOpenFoodFactsClient("")

		session := &chat.Session{
			FodmapClient:   fodmapClient,
			AllergenClient: allergenClient,
			Model:          chatModel,
			History:        history,
			Config: &genai.GenerateContentConfig{
				SystemInstruction: &genai.Content{
					Parts: []*genai.Part{{Text: systemPrompt}},
				},
				Tools: []*genai.Tool{chat.FodmapAllergenTools()},
			},
		}

		// Save user message to history.
		startSeq := len(history) * 2 // rough sequence
		userMsg := &auth.Message{
			ID:             fmt.Sprintf("msg-%s-u-%d", conv.ID, startSeq),
			ConversationID: conv.ID,
			Role:           "user",
			Content:        req.Message,
			Sequence:       startSeq,
		}
		if err := s.userStore.AddMessage(ctx, userMsg); err != nil {
			slog.Warn("chat: failed to save user message", "error", err)
		}

		stream := r.URL.Query().Get("stream") == "true"
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			flusher, ok := w.(http.Flusher)
			if !ok {
				respondError(w, "streaming not supported", http.StatusInternalServerError)
				return
			}

			onText := func(text string) {
				sseEvent := map[string]string{"type": "chunk", "text": text}
				sseData, _ := json.Marshal(sseEvent)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", sseData)
				flusher.Flush()
			}

			result, err := session.SendWithToolCalls(ctx, client, req.Message, onText)
			if err != nil {
				slog.Error("chat: send message", "error", err)
				sseEvent := map[string]string{"type": "error", "text": "chat processing failed"}
				sseData, _ := json.Marshal(sseEvent)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", sseData)
				flusher.Flush()
				return
			}
			
			// Save model response to history.
			saveModelResponse(ctx, s.userStore, conv.ID, result, startSeq+1)

			doneEvent := map[string]any{"type": "done", "tool_calls": result.ToolCalls, "conversation_id": conv.ID}
			doneData, _ := json.Marshal(doneEvent)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", doneData)
			flusher.Flush()

		} else {
			result, err := session.SendWithToolCalls(ctx, client, req.Message, nil)
			if err != nil {
				slog.Error("chat: send message", "error", err)
				respondError(w, "chat processing failed", http.StatusInternalServerError)
				return
			}

			// Save model response to history.
			saveModelResponse(ctx, s.userStore, conv.ID, result, startSeq+1)

			resp := chatResponse{
				Business:       biz,
				Answer:         result.Text,
				ToolCalls:      result.ToolCalls,
				ConversationID: conv.ID,
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}
	}
}

func messagesToContent(msgs []*auth.Message) []*genai.Content {
	var history []*genai.Content
	for _, m := range msgs {
		role := m.Role
		if role == "tool_call" || role == "tool_response" {
			// TODO: Properly reconstruct tool turns if needed.
			// Manual history management in chat.go currently focuses on text turns.
			continue
		}
		history = append(history, &genai.Content{
			Role:  role,
			Parts: []*genai.Part{{Text: m.Content}},
		})
	}
	return history
}

func saveModelResponse(ctx context.Context, store auth.Store, convID string, res chat.SendResult, seq int) {
	msg := &auth.Message{
		ID:             fmt.Sprintf("msg-%s-m-%d", convID, seq),
		ConversationID: convID,
		Role:           "model",
		Content:        res.Text,
		Sequence:       seq,
	}
	if err := store.AddMessage(ctx, msg); err != nil {
		slog.Warn("chat: failed to save model response", "error", err)
	}
}

// newGeminiChatFactory returns the production GeminiChatFactory that creates
// real Gemini API sessions.
func newGeminiChatFactory(apiKey, model string) GeminiChatFactory {
	return func(ctx context.Context, systemPrompt string) (*genai.Client, *genai.Chat, error) {
		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:  apiKey,
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			return nil, nil, err
		}
		chatSession, err := client.Chats.Create(ctx, model, &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: systemPrompt}},
			},
			Tools: []*genai.Tool{chat.FodmapAllergenTools()},
		}, nil)
		if err != nil {
			return nil, nil, err
		}
		return client, chatSession, nil
	}
}
