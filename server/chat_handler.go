package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"fodmap/chat"
	"fodmap/search"

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
	Category string `json:"category"`
	City     string `json:"city"`
	State    string `json:"state"`
}

type chatResponse struct {
	Business  chatBusinessResponse `json:"business"`
	Answer    string               `json:"answer"`
	ToolCalls []string             `json:"tool_calls"`
}

type chatBusinessResponse struct {
	Name  string `json:"name"`
	City  string `json:"city"`
	State string `json:"state"`
}

// GeminiChatFactory creates a Gemini chat session given a system prompt.
// Extracted as a function type so tests can inject stubs.
type GeminiChatFactory func(ctx context.Context, systemPrompt string) (*genai.Client, *genai.Chat, error)

func (s *Server) chatHandler(w http.ResponseWriter, r *http.Request) {
	if s.searcher == nil {
		http.Error(w, `{"error":"search service not configured"}`, http.StatusServiceUnavailable)
		return
	}
	if s.geminiFactory == nil {
		http.Error(w, `{"error":"chat service not configured"}`, http.StatusServiceUnavailable)
		return
	}

	query := r.PathValue("query")
	if query == "" {
		http.Error(w, `{"error":"search query is required"}`, http.StatusBadRequest)
		return
	}

	// Limit body size.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Message == "" {
		http.Error(w, `{"error":"message is required"}`, http.StatusBadRequest)
		return
	}

	if err := chat.ValidateChatInput(req.Message); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultChatLimit
	}

	// Enforce a request-level timeout.
	ctx, cancel := context.WithTimeout(r.Context(), chatRequestTimeout)
	defer cancel()

	// 1. Find the top business.
	filter := search.SearchFilter{
		Category: req.Category,
		City:     req.City,
		State:    req.State,
	}
	bizResult, err := s.searcher.GetBusinesses(ctx, query, 1, filter)
	if err != nil {
		slog.Error("chat: search businesses", "error", err)
		http.Error(w, `{"error":"business search failed"}`, http.StatusInternalServerError)
		return
	}
	if len(bizResult.Businesses) == 0 {
		http.Error(w, `{"error":"no businesses found for query"}`, http.StatusNotFound)
		return
	}
	biz := bizResult.Businesses[0]

	// 2. Fetch reviews for that business.
	reviewFilter := search.SearchFilter{BusinessID: biz.ID}
	reviewResult, err := s.searcher.GetReviews(ctx, query, limit, reviewFilter)
	if err != nil {
		slog.Error("chat: search reviews", "error", err)
		http.Error(w, `{"error":"review search failed"}`, http.StatusInternalServerError)
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
	chatBiz := &chat.Business{ID: biz.ID, Name: biz.Name, City: biz.City, State: biz.State}
	systemPrompt, err := chat.RenderChatSystemPrompt(chat.DefaultChatInstruction, chatBiz, reviews)
	if err != nil {
		slog.Error("chat: render prompt", "error", err)
		http.Error(w, `{"error":"failed to build chat context"}`, http.StatusInternalServerError)
		return
	}

	// 4. Create Gemini session and send the message.
	geminiClient, geminiChat, err := s.geminiFactory(ctx, systemPrompt)
	if err != nil {
		slog.Error("chat: create gemini session", "error", err)
		http.Error(w, `{"error":"failed to initialize chat"}`, http.StatusInternalServerError)
		return
	}

	// Topic pre-screen.
	if foodRelated, err := chat.IsFoodRelated(ctx, geminiClient, req.Message); err != nil {
		slog.Warn("chat: topic screen error", "error", err)
	} else if !foodRelated {
		http.Error(w, `{"error":"I can only help with food, ingredients, FODMAP, and allergen questions"}`, http.StatusBadRequest)
		return
	}

	// Build a session using the server's own searcher for FODMAP lookups via HTTP
	// (the chat package's Session needs FodmapServerClient, which talks over HTTP).
	// For the endpoint, we create an HTTP client pointed at ourselves.
	fodmapClient := chat.NewHTTPFodmapServerClient("http://" + r.Host)
	allergenClient := chat.NewOpenFoodFactsClient("")

	session := &chat.Session{
		FodmapClient:   fodmapClient,
		AllergenClient: allergenClient,
	}

	result, err := session.SendWithToolCalls(ctx, geminiChat, req.Message, nil)
	if err != nil {
		slog.Error("chat: send message", "error", err)
		http.Error(w, `{"error":"chat processing failed"}`, http.StatusInternalServerError)
		return
	}

	if result.ToolCalls == nil {
		result.ToolCalls = []string{}
	}

	resp := chatResponse{
		Business: chatBusinessResponse{
			Name:  biz.Name,
			City:  biz.City,
			State: biz.State,
		},
		Answer:    result.Text,
		ToolCalls: result.ToolCalls,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("chat: encode response", "error", err)
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
