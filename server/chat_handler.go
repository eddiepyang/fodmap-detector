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
	Message        string `json:"message"`
	Limit          int    `json:"limit"`
	Category       string `json:"category"`
	City           string `json:"city"`
	State          string `json:"state"`
	ConversationID string `json:"conversation_id"`
}

type chatResponse struct {
	Business       chatBusinessResponse `json:"business"`
	Answer         string               `json:"answer"`
	ToolCalls      []string             `json:"tool_calls"`
	ConversationID string               `json:"conversation_id"`
	ContextMessage *auth.Message        `json:"context_message,omitempty"`
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
		convID := r.PathValue("id")
		query := r.PathValue("query")

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

		// Use path param if provided.
		if convID != "" {
			req.ConversationID = convID
		}

		// We need either a query (for new chats) or a conversation ID (for existing).
		if query == "" && req.ConversationID == "" {
			respondError(w, "search query or conversation ID is required", http.StatusBadRequest)
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

		dietaryProfile := ""
		if userID != "" && userID != "anonymous" {
			if profile, err := s.userStore.GetDietaryProfile(ctx, userID); err == nil && len(profile) > 0 {
				if string(profile) != "{}" {
					dietaryProfile = string(profile)
				}
			}
		}

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
			// Legacy fallback: if no conversation was found, but we have a query (from legacy path),
			// try to create a new one on the fly.
			if query != "" {
				bizResult, err := s.searcher.GetBusinesses(ctx, query, 1, search.SearchFilter{})
				if err != nil || len(bizResult.Businesses) == 0 {
					respondError(w, "business search failed or no businesses found", http.StatusNotFound)
					return
				}
				b := bizResult.Businesses[0]

				if userID == "" {
					userID = "anonymous"
				}

				conv = &auth.Conversation{
					ID:         "legacy-" + uuid.New().String(),
					UserID:     userID,
					BusinessID: b.ID,
					Title:      "Legacy chat about " + b.Name,
				}

				if err := s.userStore.CreateConversation(ctx, conv); err != nil {
					slog.Error("failed to create auto conversation", "error", err)
					respondError(w, "failed to create conversation", http.StatusInternalServerError)
					return
				}
				slog.Info("auto-created legacy conversation", "id", conv.ID, "business", b.Name)
			} else {
				// This block should theoretically never be reached under the new architecture
				// since frontend creates conv first via createConversationHandler.
				respondError(w, "conversation not found", http.StatusBadRequest)
				return
			}
		}

		var reviews []chat.Review
		if conv.BusinessID != "" && conv.BusinessID != "general" {
			// Existing conversation: rebuild system prompt from business ID.
			slog.Info("chat: reloading business context", "business_id", conv.BusinessID, "id", conv.ID)
			b, err := s.searcher.GetBusinesses(ctx, "", 1, search.SearchFilter{BusinessID: conv.BusinessID})
			if err != nil || len(b.Businesses) == 0 {
				slog.Warn("chat: failed to reload business context, using formatted fallback", "error", err)
				biz = chatBusinessResponse{Name: "this restaurant", City: "local area"}
				chatBiz := &chat.Business{Name: biz.Name, City: biz.City}
				systemPrompt, _ = chat.RenderChatSystemPrompt(chat.DefaultChatInstruction, chatBiz, dietaryProfile)
			} else {
				biz = chatBusinessResponse{Name: b.Businesses[0].Name, City: b.Businesses[0].City, State: b.Businesses[0].State}
				chatBiz := &chat.Business{ID: conv.BusinessID, Name: biz.Name, City: biz.City, State: biz.State}
				slog.Info("chat: business context loaded", "name", biz.Name)

				var reviewResult search.SearchReviews
				var err error

				if len(conv.ReviewContext) > 0 {
					slog.Info("chat: reloading specific review context", "count", len(conv.ReviewContext))
					ids := make([]string, len(conv.ReviewContext))
					for i, rc := range conv.ReviewContext {
						ids[i] = rc.ID
					}
					reviewResult, err = s.searcher.GetReviews(ctx, "", len(ids), search.SearchFilter{ReviewIDs: ids})

					// Re-apply original scores if found
					if err == nil {
						scoreMap := make(map[string]float64)
						for _, rc := range conv.ReviewContext {
							scoreMap[rc.ID] = rc.Score
						}
						for i := range reviewResult.BusinessReviews {
							if s, ok := scoreMap[reviewResult.BusinessReviews[i].Review.Review.ReviewID]; ok {
								reviewResult.BusinessReviews[i].Score = s
							}
						}
					}
				} else {
					slog.Info("chat: reloading general business reviews (legacy)", "business_id", conv.BusinessID)
					reviewResult, err = s.searcher.GetReviews(ctx, "", limit, search.SearchFilter{BusinessID: conv.BusinessID})
				}

				if err == nil {
					slog.Info("chat: reviews loaded", "count", len(reviewResult.BusinessReviews))
					for _, rr := range reviewResult.BusinessReviews {
						reviews = append(reviews, chat.Review{
							Stars: rr.Review.Review.Stars,
							Text:  rr.Review.Review.Text,
						})
					}
				} else {
					slog.Warn("chat: failed to load reviews", "error", err)
				}

				systemPrompt, err = chat.RenderChatSystemPrompt(chat.DefaultChatInstruction, chatBiz, dietaryProfile)
				if err != nil {
					slog.Warn("chat: render prompt failed, using name-only fallback", "error", err)
					systemPrompt = fmt.Sprintf("You are a FODMAP and food allergen expert helping people understand dishes at %s (%s, %s).", biz.Name, biz.City, biz.State)
				}
			}
		} else {
			// General inquiry without a specific business context.
			slog.Info("chat: using general assistant mode", "id", conv.ID, "business_id", conv.BusinessID)
			chatBiz := &chat.Business{Name: "General Assistant", City: "Anywhere"}
			systemPrompt, _ = chat.RenderChatSystemPrompt(chat.DefaultChatInstruction, chatBiz, dietaryProfile)
		}

		chatModel := s.chatModel
		if chatModel == "" {
			chatModel = "gemini-3-flash-lite-preview"
		}

		if client == nil {
			respondError(w, "chat service not configured", http.StatusServiceUnavailable)
			return
		}

		// Topic pre-screen.
		isFollowUp := len(history) > 0
		if foodRelated, err := chat.IsFoodRelated(ctx, client, s.filterModel, req.Message, isFollowUp); err != nil {
			slog.Warn("chat: topic screen error", "error", err)
		} else if !foodRelated {
			respondError(w, "I can only help with food, ingredients, FODMAP, and allergen questions", http.StatusBadRequest)
			return
		}

		// Build a session using the server's own searcher for FODMAP lookups.
		fodmapClient := NewDirectFodmapClient(s)
		allergenClient := chat.NewOpenFoodFactsClient("")

		// Legacy fallback: generate review context for conversations created
		// before summary generation was moved to createConversationHandler.
		var contextMsg *auth.Message
		if len(history) == 0 && len(reviews) > 0 {
			contextContent := chat.FormatReviewsContext(biz.Name, reviews)
			history = append(history, &genai.Content{
				Role:  "model",
				Parts: []*genai.Part{{Text: contextContent}},
			})

			contextMsg = &auth.Message{
				ID:             fmt.Sprintf("msg-%s-ctx", conv.ID),
				ConversationID: conv.ID,
				Role:           "model",
				Content:        contextContent,
				Sequence:       0,
				CreatedAt:      time.Now(),
			}
			if err := s.userStore.AddMessage(ctx, contextMsg); err != nil {
				slog.Warn("chat: failed to save context message", "error", err)
			}
		}

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
		startSeq := len(history)
		userMsg := &auth.Message{
			ID:             fmt.Sprintf("msg-%s-u-%d", conv.ID, startSeq),
			ConversationID: conv.ID,
			Role:           "user",
			Content:        req.Message,
			Sequence:       startSeq,
			CreatedAt:      time.Now(),
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

			// If we injected a context message, send it first.
			if contextMsg != nil {
				contextEvent := map[string]any{"type": "message", "message": contextMsg}
				contextData, _ := json.Marshal(contextEvent)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", contextData)
				flusher.Flush()
			}

			onText := func(text string) {
				sseEvent := map[string]string{"type": "chunk", "text": text}
				sseData, _ := json.Marshal(sseEvent)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", sseData)
				flusher.Flush()
			}

			onToolCall := func(calls []string) {
				sseEvent := map[string]any{"type": "tool", "tool_calls": calls}
				sseData, _ := json.Marshal(sseEvent)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", sseData)
				flusher.Flush()
			}

			result, err := session.SendWithToolCalls(ctx, client, req.Message, onText, onToolCall)
			if err != nil {
				slog.Error("chat: send message", "error", err)
				sseEvent := map[string]string{"type": "error", "text": "chat processing failed"}
				sseData, _ := json.Marshal(sseEvent)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", sseData)
				flusher.Flush()
				return
			}

			// Save tool turns then model response to history.
			modelSeq := saveToolTurns(ctx, s.userStore, conv.ID, result, startSeq+1)
			modelMsg := saveModelResponse(ctx, s.userStore, conv.ID, result, modelSeq)

			doneEvent := map[string]any{
				"type":            "done",
				"tool_calls":      result.ToolCalls,
				"conversation_id": conv.ID,
				"message":         modelMsg,
			}
			doneData, _ := json.Marshal(doneEvent)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", doneData)
			flusher.Flush()

		} else {
			result, err := session.SendWithToolCalls(ctx, client, req.Message, nil, nil)
			if err != nil {
				slog.Error("chat: send message", "error", err)
				respondError(w, "chat processing failed", http.StatusInternalServerError)
				return
			}

			// Save tool turns then model response to history.
			modelSeq := saveToolTurns(ctx, s.userStore, conv.ID, result, startSeq+1)
			_ = saveModelResponse(ctx, s.userStore, conv.ID, result, modelSeq)

			resp := chatResponse{
				Business:       biz,
				Answer:         result.Text,
				ToolCalls:      result.ToolCalls,
				ConversationID: conv.ID,
				ContextMessage: contextMsg,
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}
	}
}

func messagesToContent(msgs []*auth.Message) []*genai.Content {
	var history []*genai.Content
	for _, m := range msgs {
		switch m.Role {
		case "tool_call":
			var calls []chat.ToolCallEntry
			if err := json.Unmarshal([]byte(m.Content), &calls); err != nil {
				slog.Warn("chat: failed to parse tool_call message", "id", m.ID, "error", err)
				continue
			}
			c := &genai.Content{Role: "model"}
			for _, call := range calls {
				c.Parts = append(c.Parts, genai.NewPartFromFunctionCall(call.Name, call.Args))
			}
			history = append(history, c)
		case "tool_response":
			var responses []chat.ToolResponseEntry
			if err := json.Unmarshal([]byte(m.Content), &responses); err != nil {
				slog.Warn("chat: failed to parse tool_response message", "id", m.ID, "error", err)
				continue
			}
			c := &genai.Content{Role: "user"}
			for _, resp := range responses {
				c.Parts = append(c.Parts, genai.NewPartFromFunctionResponse(resp.Name, resp.Result))
			}
			history = append(history, c)
		default:
			history = append(history, &genai.Content{
				Role:  m.Role,
				Parts: []*genai.Part{{Text: m.Content}},
			})
		}
	}
	return history
}

// saveToolTurns persists each tool call/response pair from result to the store,
// starting at seq. It returns the next available sequence number.
func saveToolTurns(ctx context.Context, store auth.Store, convID string, result chat.SendResult, seq int) int {
	for _, turn := range result.ToolTurns {
		callsJSON, err := json.Marshal(turn.Calls)
		if err != nil {
			slog.Warn("chat: failed to marshal tool calls", "error", err)
			seq += 2
			continue
		}
		respJSON, err := json.Marshal(turn.Responses)
		if err != nil {
			slog.Warn("chat: failed to marshal tool responses", "error", err)
			seq += 2
			continue
		}
		callMsg := &auth.Message{
			ID:             fmt.Sprintf("msg-%s-tc-%d", convID, seq),
			ConversationID: convID,
			Role:           "tool_call",
			Content:        string(callsJSON),
			Sequence:       seq,
		}
		if err := store.AddMessage(ctx, callMsg); err != nil {
			slog.Warn("chat: failed to save tool call", "error", err)
		}
		seq++
		respMsg := &auth.Message{
			ID:             fmt.Sprintf("msg-%s-tr-%d", convID, seq),
			ConversationID: convID,
			Role:           "tool_response",
			Content:        string(respJSON),
			Sequence:       seq,
		}
		if err := store.AddMessage(ctx, respMsg); err != nil {
			slog.Warn("chat: failed to save tool response", "error", err)
		}
		seq++
	}
	return seq
}

func saveModelResponse(ctx context.Context, store auth.Store, convID string, res chat.SendResult, seq int) *auth.Message {
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
	return msg
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
