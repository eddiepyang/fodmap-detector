// Package chat provides shared types, interfaces, and logic for FODMAP/allergen
// chat sessions backed by Gemini. Used by both the CLI and the HTTP server.
package chat

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"google.golang.org/genai"
)

const (
	ScreenGeminiModel = "gemini-3.1-flash-lite-preview"
	MaxInputLen       = 2000
	MaxResponseLen    = 4000
)

//go:embed chat-instruction.txt
var DefaultChatInstruction string

// InjectionPatterns are case-insensitive substrings used as a first-line defense
// against common prompt injection attempts.
var InjectionPatterns = []string{
	"ignore previous instructions",
	"ignore all previous",
	"forget your instructions",
	"disregard your instructions",
	"you are now",
	"pretend you are",
	"<|system|>",
	"<|im_start|>",
}

// offBaseURL is the Open Food Facts search endpoint. Exposed as a package-level
// var so tests can redirect it to an httptest.Server without mocking.
var offBaseURL = "https://world.openfoodfacts.org/cgi/search.pl"

// ---- domain types ----

type Business struct {
	ID    string
	Name  string
	City  string
	State string
}

type Review struct {
	ReviewID string  `json:"review_id"`
	Stars    float32 `json:"Stars"`
	Text     string  `json:"Text"`
}

// ---- network interfaces ----

// FodmapSessionClient is the minimal interface required by Session to dispatch
// FODMAP tool calls during a chat turn.
type FodmapSessionClient interface {
	LookupFODMAP(ctx context.Context, ingredient string) (FodmapToolResponse, error)
}

// FodmapServerClient is the full client interface used by CLI pre-chat setup
// (business search and review fetch) in addition to in-session lookups.
type FodmapServerClient interface {
	FodmapSessionClient
	FetchTopBusiness(ctx context.Context, query, category, city, state string) (*Business, error)
	FetchChatReviews(ctx context.Context, businessID, query string, limit int) ([]Review, error)
}

type AllergenClient interface {
	LookupAllergens(ctx context.Context, ingredient string) (AllergenToolResponse, error)
}

// ---- tool response types ----

type FodmapToolResponse struct {
	Ingredient   string   `json:"ingredient"`
	Found        bool     `json:"found"`
	FodmapLevel  string   `json:"fodmap_level,omitempty"`
	FodmapGroups []string `json:"fodmap_groups,omitempty"`
	Notes        string   `json:"notes,omitempty"`
	Message      string   `json:"message,omitempty"`
	Error        string   `json:"error,omitempty"`
}

type AllergenToolResponse struct {
	Ingredient string   `json:"ingredient"`
	Allergens  []string `json:"allergens,omitempty"`
	Source     string   `json:"source,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// ---- guardrails ----

func ValidateChatInput(input string) error {
	if len(input) > MaxInputLen {
		return fmt.Errorf("message too long (max %d characters)", MaxInputLen)
	}
	lower := strings.ToLower(input)
	for _, p := range InjectionPatterns {
		if strings.Contains(lower, p) {
			return fmt.Errorf("message contains disallowed content")
		}
	}
	return nil
}

// IsFoodRelated runs a lightweight single-turn Gemini call to check whether the
// user's message is on-topic. Fails open on error to avoid blocking valid queries.
func IsFoodRelated(ctx context.Context, client *genai.Client, model, input string, isFollowUp bool) (bool, error) {
	if model == "" {
		model = ScreenGeminiModel
	}

	prompt := fmt.Sprintf(
		"Is the following message asking about food, restaurants, ingredients, dietary restrictions, allergens, or FODMAP content? Answer with exactly \"yes\" or \"no\".\nMessage: %q",
		input,
	)
	if isFollowUp {
		prompt = fmt.Sprintf(
			"The user is already in a conversation about a restaurant. Is the following follow-up message still potentially relevant to the food/restaurant topic? Answer with exactly \"yes\" or \"no\".\nMessage: %q",
			input,
		)
	}

	resp, err := client.Models.GenerateContent(ctx, model, genai.Text(prompt), nil)
	if err != nil {
		return true, fmt.Errorf("topic screen: %w", err)
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(resp.Text())), "y"), nil
}

// ---- chat session (tool-call loop) ----

// Session manages manual history and tool dispatch for a Gemini chat.
// We manage history manually to bypass a bug in the genai.Chat SDK where
// stream chunks are recorded as separate model turns, corrupting the
// tool-call sequence.
type Session struct {
	FodmapClient   FodmapSessionClient
	AllergenClient AllergenClient
	Model          string
	Config         *genai.GenerateContentConfig
	History        []*genai.Content
}

// ToolCallEntry records a single function call made during a chat turn.
type ToolCallEntry struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// ToolResponseEntry records the result of a single function call.
type ToolResponseEntry struct {
	Name   string         `json:"name"`
	Result map[string]any `json:"result"`
}

// ToolTurn groups the function calls and their responses from one iteration
// of the tool-call loop.
type ToolTurn struct {
	Calls     []ToolCallEntry     `json:"calls"`
	Responses []ToolResponseEntry `json:"responses"`
}

// SendResult contains the outcome of a chat turn.
type SendResult struct {
	Text      string
	ToolCalls []string
	ToolTurns []ToolTurn
}

// SendWithToolCalls sends a user message and iterates until the model returns a
// plain-text response, dispatching any function calls in between.
// The optional onText callback is invoked for each streamed text chunk.
func (s *Session) SendWithToolCalls(ctx context.Context, client *genai.Client, input string, onText func(string)) (SendResult, error) {
	if input != "" {
		s.History = append(s.History, &genai.Content{
			Role:  "user",
			Parts: []*genai.Part{{Text: input}},
		})
	}

	var result SendResult
	var fullText strings.Builder

	for {
		// Prepare a single Content for this model turn.
		modelTurn := &genai.Content{Role: "model"}

		// 1. Initial/Streaming Turn
		for resp, err := range client.Models.GenerateContentStream(ctx, s.Model, s.History, s.Config) {
			if err != nil {
				return SendResult{}, fmt.Errorf("stream error: %w", err)
			}
			if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
				continue
			}
			// Merge parts from this chunk into our single model turn.
			for _, part := range resp.Candidates[0].Content.Parts {
				// Skip empty parts that stream chunks sometimes include.
				if part.Text == "" && part.FunctionCall == nil && part.FunctionResponse == nil &&
					part.InlineData == nil && part.FileData == nil &&
					part.ExecutableCode == nil && part.CodeExecutionResult == nil {
					continue
				}

				// Record for history
				modelTurn.Parts = append(modelTurn.Parts, part)

				// Handle display/result
				if part.Text != "" {
					fullText.WriteString(part.Text)
					if onText != nil {
						onText(part.Text)
					}
				}
				if part.FunctionCall != nil {
					if onText != nil {
						onText(fmt.Sprintf("\n[Tool Call] %s\n", part.FunctionCall.Name))
					}
				}
			}
		}

		// Record the complete model turn (all chunks merged) into history.
		s.History = append(s.History, modelTurn)

		// 2. Scan model turn for tool calls
		var pendingCalls []struct {
			Name string
			Args map[string]any
		}
		for _, p := range modelTurn.Parts {
			if p.FunctionCall != nil {
				pendingCalls = append(pendingCalls, struct {
					Name string
					Args map[string]any
				}{Name: p.FunctionCall.Name, Args: p.FunctionCall.Args})
			}
		}

		if len(pendingCalls) == 0 {
			break
		}

		// 3. Dispatch tool calls and add response turn
		responseTurn := &genai.Content{Role: "user"}
		turn := ToolTurn{}
		for _, call := range pendingCalls {
			toolResult := s.DispatchTool(ctx, call.Name, call.Args)
			resultMap := ToMap(toolResult)
			responseTurn.Parts = append(responseTurn.Parts,
				genai.NewPartFromFunctionResponse(call.Name, resultMap),
			)
			result.ToolCalls = append(result.ToolCalls, fmt.Sprintf("%s(%v)", call.Name, call.Args["ingredient"]))
			turn.Calls = append(turn.Calls, ToolCallEntry{Name: call.Name, Args: call.Args})
			turn.Responses = append(turn.Responses, ToolResponseEntry{Name: call.Name, Result: resultMap})
		}
		result.ToolTurns = append(result.ToolTurns, turn)
		s.History = append(s.History, responseTurn)

		// Loop back to get the model's reaction to the tool responses.
	}

	result.Text = fullText.String()
	return result, nil
}

func (s *Session) DispatchTool(ctx context.Context, name string, args map[string]any) any {
	ingredient, _ := args["ingredient"].(string)
	switch name {
	case "lookup_fodmap":
		result, err := s.FodmapClient.LookupFODMAP(ctx, ingredient)
		if err != nil {
			slog.Warn("fodmap lookup failed", "ingredient", ingredient, "error", err)
			return FodmapToolResponse{Ingredient: ingredient, Found: false, Error: err.Error()}
		}
		return result
	case "lookup_allergens":
		result, err := s.AllergenClient.LookupAllergens(ctx, ingredient)
		if err != nil {
			slog.Warn("allergen lookup failed", "ingredient", ingredient, "error", err)
			return AllergenToolResponse{Ingredient: ingredient, Error: err.Error()}
		}
		return result
	default:
		return map[string]any{"error": "unknown tool: " + name}
	}
}

func ToMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// ---- tool declarations ----

func FodmapAllergenTools() *genai.Tool {
	ingredientParam := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"ingredient": {
				Type:        genai.TypeString,
				Description: "The food ingredient name to look up (e.g. \"garlic\", \"wheat\", \"milk\")",
			},
		},
		Required: []string{"ingredient"},
	}
	return &genai.Tool{
		FunctionDeclarations: []*genai.FunctionDeclaration{
			{
				Name:        "lookup_fodmap",
				Description: "Look up the FODMAP classification for a food ingredient. Returns FODMAP groups present and whether the ingredient is high, moderate, or low FODMAP.",
				Parameters:  ingredientParam,
			},
			{
				Name:        "lookup_allergens",
				Description: "Look up common allergens for a food ingredient using the Open Food Facts database.",
				Parameters:  ingredientParam,
			},
		},
	}
}

// ---- system prompt rendering ----

type PromptData struct {
	BusinessName string
	City         string
	State        string
}

func RenderChatSystemPrompt(tmplStr string, biz *Business) (string, error) {
	tmpl, err := template.New("chat").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parsing instruction template: %w", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, PromptData{
		BusinessName: biz.Name,
		City:         biz.City,
		State:        biz.State,
	}); err != nil {
		return "", fmt.Errorf("executing prompt: %w", err)
	}
	return buf.String(), nil
}

// FormatReviewsContext builds a context message that establishes the model's
// grounding in specific customer reviews.
func FormatReviewsContext(bizName string, reviews []Review) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Here's what %d customers are saying about %s:\n\n", len(reviews), bizName)
	for i, r := range reviews {
		stars := strings.Repeat("\u2605", int(r.Stars))
		if half := r.Stars - float32(int(r.Stars)); half >= 0.5 {
			stars += "\u00BD"
		}
		fmt.Fprintf(&sb, "%d. %s\n%s\n\n", i+1, stars, r.Text)
	}
	return sb.String()
}

// summarizeReviewsPrompt is the single-turn Gemini prompt used by SummarizeReviews.
// First %s is the business name; second %s is the raw review context from FormatReviewsContext.
const summarizeReviewsPrompt = `You are a concise food analyst. Given the following customer reviews for %s, produce a structured summary focused exclusively on dishes and menu items.

For each dish or menu item mentioned across the reviews:
- Name the dish
- Show the average star rating from reviews that mention it (e.g. ★4.5 avg)
- Note any recurring descriptions about taste, ingredients, or preparation

Format your output into a the following format:

	*Summary of customer feedback on dishes at %s*

	1. Dish Name A - ★4.5 avg
	   - Description 1
	   - Description 2

	2. Dish Name B - ★3.0 avg
	   - Description 1
	   - Description 2
If the reviews do not mention specific dishes, extract any general feedback about the menu as a whole. Be concise and focus on actionable insights about the food. Do not include any information about service, ambiance, or other non-food topics.`

// SummarizeReviews calls Gemini to produce a dish-focused summary of the provided
// customer reviews, suitable for injection as a model context message at the start
// of a chat history. On error, callers should fall back to FormatReviewsContext.
func SummarizeReviews(ctx context.Context, client *genai.Client, model, bizName string, reviews []Review) (string, error) {
	if len(reviews) == 0 {
		return "", fmt.Errorf("summarize reviews: no reviews provided")
	}
	if model == "" {
		model = ScreenGeminiModel
	}
	prompt := fmt.Sprintf(summarizeReviewsPrompt, bizName, FormatReviewsContext(bizName, reviews))
	resp, err := client.Models.GenerateContent(ctx, model, genai.Text(prompt), nil)
	if err != nil {
		return "", fmt.Errorf("summarize reviews: %w", err)
	}
	return strings.TrimSpace(resp.Text()), nil
}

// ---- HTTP Clients ----

type HTTPFodmapServerClient struct {
	serverURL string
	client    *http.Client
}

func NewHTTPFodmapServerClient(serverURL string) *HTTPFodmapServerClient {
	return &HTTPFodmapServerClient{
		serverURL: serverURL,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *HTTPFodmapServerClient) FetchTopBusiness(ctx context.Context, query, category, city, state string) (*Business, error) {
	u := c.serverURL + "/api/v1/search/businesses/" + url.PathEscape(query) + "?limit=1"
	if category != "" {
		u += "&category=" + url.QueryEscape(category)
	}
	if city != "" {
		u += "&city=" + url.QueryEscape(city)
	}
	if state != "" {
		u += "&state=" + url.QueryEscape(state)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var body struct {
		Businesses []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			City  string `json:"city"`
			State string `json:"state"`
		} `json:"businesses"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding business response: %w", err)
	}
	if len(body.Businesses) == 0 {
		return nil, fmt.Errorf("no businesses found for query %q", query)
	}
	b := body.Businesses[0]
	return &Business{ID: b.ID, Name: b.Name, City: b.City, State: b.State}, nil
}

func (c *HTTPFodmapServerClient) FetchChatReviews(ctx context.Context, businessID, query string, limit int) ([]Review, error) {
	u := c.serverURL + "/api/v1/search/reviews/" + url.PathEscape(query) + "?business_id=" + url.QueryEscape(businessID) + "&limit=" + strconv.Itoa(limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		Reviews []Review `json:"reviews"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decoding reviews response: %w", err)
	}
	return data.Reviews, nil
}

func (c *HTTPFodmapServerClient) LookupFODMAP(ctx context.Context, ingredient string) (FodmapToolResponse, error) {
	u := c.serverURL + "/api/v1/search/fodmap/" + url.PathEscape(ingredient)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return FodmapToolResponse{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return FodmapToolResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return FodmapToolResponse{
			Ingredient: ingredient,
			Found:      false,
			Message:    "ingredient not in database; consult the Monash University FODMAP app for accurate classification",
		}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return FodmapToolResponse{}, fmt.Errorf("server returned %d", resp.StatusCode)
	}

	var data struct {
		Level  string   `json:"level"`
		Groups []string `json:"groups"`
		Notes  string   `json:"notes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return FodmapToolResponse{}, err
	}

	return FodmapToolResponse{
		Ingredient:   ingredient,
		Found:        true,
		FodmapLevel:  data.Level,
		FodmapGroups: data.Groups,
		Notes:        data.Notes,
	}, nil
}

type OpenFoodFactsClient struct {
	baseURL  string
	client   *http.Client
	cache    sync.Map
	mu       sync.Mutex    // serializes requests to avoid rate-limiting
	lastCall time.Time     // tracks last request time
	minDelay time.Duration // minimum delay between requests
}

func NewOpenFoodFactsClient(baseURL string) *OpenFoodFactsClient {
	if baseURL == "" {
		baseURL = offBaseURL
	}
	return &OpenFoodFactsClient{
		baseURL:  baseURL,
		client:   &http.Client{Timeout: 10 * time.Second},
		minDelay: 500 * time.Millisecond,
	}
}

func (c *OpenFoodFactsClient) LookupAllergens(ctx context.Context, ingredient string) (AllergenToolResponse, error) {
	if cached, ok := c.cache.Load(ingredient); ok {
		return cached.(AllergenToolResponse), nil
	}

	// Rate-limit: serialize requests and enforce minimum delay
	c.mu.Lock()
	if elapsed := time.Since(c.lastCall); elapsed < c.minDelay {
		time.Sleep(c.minDelay - elapsed)
	}
	c.lastCall = time.Now()
	c.mu.Unlock()
	searchURL := c.baseURL + "?search_terms=" +
		url.QueryEscape(ingredient) +
		"&search_simple=1&action=process&json=1&page_size=3"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return AllergenToolResponse{}, fmt.Errorf("building OFF request: %w", err)
	}
	req.Header.Set("User-Agent", "fodmap-detector/1.0")
	resp, err := c.client.Do(req)
	if err != nil {
		return AllergenToolResponse{}, fmt.Errorf("OFF request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return AllergenToolResponse{
			Ingredient: ingredient,
			Error:      fmt.Sprintf("Open Food Facts returned status %d", resp.StatusCode),
		}, nil
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") && !strings.Contains(ct, "text/json") {
		return AllergenToolResponse{
			Ingredient: ingredient,
			Error:      "Open Food Facts returned non-JSON response; service may be temporarily unavailable",
		}, nil
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Products []struct {
			AllergensTags []string `json:"allergens_tags"`
		} `json:"products"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return AllergenToolResponse{
			Ingredient: ingredient,
			Error:      "could not parse Open Food Facts response",
		}, nil
	}

	seen := make(map[string]bool)
	allergens := []string{}
	for _, p := range result.Products {
		for _, tag := range p.AllergensTags {
			clean := strings.TrimPrefix(tag, "en:")
			if !seen[clean] {
				seen[clean] = true
				allergens = append(allergens, clean)
			}
		}
	}
	res := AllergenToolResponse{
		Ingredient: ingredient,
		Allergens:  allergens,
		Source:     "Open Food Facts",
	}
	c.cache.Store(ingredient, res)
	return res, nil
}
