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

// Gemini model and length limit constants for the chat session.
const (
	ScreenGeminiModel = "gemini-3.1-flash-lite-preview"
	MaxInputLen       = 2000
	MaxResponseLen    = 4000
)

// DefaultChatInstruction is the default system prompt template, embedded from
// chat-instruction.txt. Callers may override it via RenderChatSystemPrompt.
//
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

// Business is a restaurant or food business returned by the search server.
type Business struct {
	ID    string
	Name  string
	City  string
	State string
}

// Review is a single restaurant review fetched for chat context.
type Review struct {
	ReviewID string  `json:"review_id"`
	Stars    float32 `json:"Stars"`
	Text     string  `json:"Text"`
}

// ---- network interfaces ----

// FodmapSessionClient is the minimal interface required by Session to dispatch
// FODMAP tool calls during a chat turn.
type FodmapSessionClient interface {
	LookupFodmap(ctx context.Context, ingredient string) (FodmapToolResponse, error)
}

// FodmapServerClient is the full client interface used by CLI pre-chat setup
// (business search and review fetch) in addition to in-session lookups.
type FodmapServerClient interface {
	FodmapSessionClient
	FetchTopBusiness(ctx context.Context, query, category, city, state string) (*Business, error)
	FetchChatReviews(ctx context.Context, businessID, query string, limit int) ([]Review, error)
}

// AllergenClient provides allergen information for a given ingredient.
type AllergenClient interface {
	LookupAllergens(ctx context.Context, ingredient string) (AllergenToolResponse, error)
}

// ProductIngredientClient fetches the ingredient names that make up a packaged
// product (e.g. from Open Food Facts) so each can be classified individually.
type ProductIngredientClient interface {
	LookupProductIngredients(ctx context.Context, product string) ([]string, error)
}

// ---- tool response types ----

// FodmapToolResponse is the result of a FODMAP lookup tool call.
type FodmapToolResponse struct {
	Ingredient    string   `json:"ingredient"`
	Found         bool     `json:"found"`
	FodmapLevel   string   `json:"fodmap_level,omitempty"`
	FodmapGroups  []string `json:"fodmap_groups,omitempty"`
	Notes         string   `json:"notes,omitempty"`
	Substitutions []string `json:"substitutions,omitempty"`
	Message       string   `json:"message,omitempty"`
	Error         string   `json:"error,omitempty"`
}

// AllergenToolResponse is the result of an allergen lookup tool call.
type AllergenToolResponse struct {
	Ingredient string   `json:"ingredient"`
	Allergens  []string `json:"allergens,omitempty"`
	Source     string   `json:"source,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// IngredientFodmap pairs a product ingredient with its FODMAP classification.
type IngredientFodmap struct {
	Ingredient string   `json:"ingredient"`
	Found      bool     `json:"found"`
	Level      string   `json:"level,omitempty"`
	Groups     []string `json:"groups,omitempty"`
}

// ProductFodmapResponse is the aggregated FODMAP assessment of a packaged
// product, derived by classifying each of its ingredients. OverallLevel is the
// worst case across the ingredients found in the FODMAP database.
type ProductFodmapResponse struct {
	Product      string             `json:"product"`
	OverallLevel string             `json:"overall_level,omitempty"`
	Groups       []string           `json:"groups,omitempty"`
	Ingredients  []IngredientFodmap `json:"ingredients,omitempty"`
	Unknown      []string           `json:"unknown,omitempty"`
	Message      string             `json:"message,omitempty"`
	Error        string             `json:"error,omitempty"`
}

// ---- guardrails ----

// ValidateChatInput checks user input against length limits and injection patterns.
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
	ProductClient  ProductIngredientClient
	Backend        ChatBackend
	SystemPrompt   string
	Tools          []ToolDeclaration
	History        []Message
}

// ToolCallEntry records a single function call made during a chat turn.
type ToolCallEntry struct {
	Name             string         `json:"name"`
	Args             map[string]any `json:"args"`
	ThoughtSignature string         `json:"thought_signature,omitempty"`
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
// The optional onToolCall callback is invoked when the model requests tool calls.
func (s *Session) SendWithToolCalls(ctx context.Context, input string, onText func(string), onToolCall func([]string)) (SendResult, error) {
	if input != "" {
		s.History = append(s.History, Message{
			Role: "user",
			Text: input,
		})
	}

	var result SendResult
	var fullText strings.Builder

	for {
		opts := GenerateOpts{
			SystemPrompt: s.SystemPrompt,
			History:      s.History,
			Tools:        s.Tools,
			OnText:       onText,
			OnToolCall:   onToolCall,
		}

		// 1. Generate model response
		modelMsg, err := s.Backend.Generate(ctx, opts)
		if err != nil {
			return SendResult{}, fmt.Errorf("model generation error: %w", err)
		}

		// Record the complete model turn into history.
		s.History = append(s.History, modelMsg)
		fullText.WriteString(modelMsg.Text)

		// 2. Scan model turn for tool calls
		if len(modelMsg.FunctionCalls) == 0 {
			break
		}

		// 3. Dispatch tool calls and add response turn
		responseMsg := Message{Role: "user"}
		turn := ToolTurn{}

		for _, call := range modelMsg.FunctionCalls {
			toolResult := s.DispatchTool(ctx, call.Name, call.Args)
			resultMap := ToMap(toolResult)

			responseMsg.FunctionResults = append(responseMsg.FunctionResults, FunctionResult{
				Name:   call.Name,
				Result: resultMap,
			})

			result.ToolCalls = append(result.ToolCalls, fmt.Sprintf("%s(%v)", call.Name, call.Args["ingredient"]))
			turn.Calls = append(turn.Calls, ToolCallEntry(call))
			turn.Responses = append(turn.Responses, ToolResponseEntry{Name: call.Name, Result: resultMap})
		}

		result.ToolTurns = append(result.ToolTurns, turn)
		s.History = append(s.History, responseMsg)

		// Loop back to get the model's reaction to the tool responses.
	}

	result.Text = fullText.String()
	return result, nil
}

// DispatchTool invokes a named tool with the given arguments and returns the
// tool's response to be included in the chat turn.
func (s *Session) DispatchTool(ctx context.Context, name string, args map[string]any) any {
	ingredient, _ := args["ingredient"].(string)
	switch name {
	case "lookup_fodmap":
		result, err := s.FodmapClient.LookupFodmap(ctx, ingredient)
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
	case "lookup_product_fodmap":
		product, _ := args["product"].(string)
		if s.ProductClient == nil {
			return ProductFodmapResponse{Product: product, Error: "product lookup is not configured"}
		}
		return AnalyzeProductFodmap(ctx, s.ProductClient, s.FodmapClient, product)
	default:
		return map[string]any{"error": "unknown tool: " + name}
	}
}

// ToMap converts a value to a map[string]any via JSON round-trip. This is
// used to normalize tool response structs for Gemini's function-calling API.
// Marshal/unmarshal errors are ignored because the input is always a struct
// with JSON-tagged fields.
func ToMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// fodmapLevelRank orders FODMAP levels by severity so the worst case across a
// product's ingredients can be computed. Unknown levels rank 0.
func fodmapLevelRank(level string) int {
	switch level {
	case "low":
		return 1
	case "moderate":
		return 2
	case "high":
		return 3
	default:
		return 0
	}
}

// AnalyzeProductFodmap fetches a packaged product's ingredient list via the
// ProductIngredientClient and classifies each ingredient with the FODMAP
// client, returning the worst-case level and the union of FODMAP groups found.
// Ingredients absent from the FODMAP database are reported under Unknown and do
// not affect the overall level.
func AnalyzeProductFodmap(ctx context.Context, products ProductIngredientClient, fodmap FodmapSessionClient, product string) ProductFodmapResponse {
	resp := ProductFodmapResponse{Product: product}

	ingredients, err := products.LookupProductIngredients(ctx, product)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	if len(ingredients) == 0 {
		resp.Message = "no ingredients found for product; it may not be in Open Food Facts"
		return resp
	}

	worst := 0
	seenGroup := make(map[string]bool)
	for _, ing := range ingredients {
		res, err := fodmap.LookupFodmap(ctx, ing)
		if err != nil || !res.Found {
			resp.Unknown = append(resp.Unknown, ing)
			continue
		}
		resp.Ingredients = append(resp.Ingredients, IngredientFodmap{
			Ingredient: ing,
			Found:      true,
			Level:      res.FodmapLevel,
			Groups:     res.FodmapGroups,
		})
		if r := fodmapLevelRank(res.FodmapLevel); r > worst {
			worst = r
		}
		for _, g := range res.FodmapGroups {
			if !seenGroup[g] {
				seenGroup[g] = true
				resp.Groups = append(resp.Groups, g)
			}
		}
	}

	switch worst {
	case 3:
		resp.OverallLevel = "high"
	case 2:
		resp.OverallLevel = "moderate"
	case 1:
		resp.OverallLevel = "low"
	default:
		resp.Message = "no ingredients matched the FODMAP database; consult the Monash University FODMAP app"
	}
	return resp
}

// ---- tool declarations ----

// FodmapAllergenTools returns the tool declarations for FODMAP and allergen
// lookups, suitable for passing to Gemini's function-calling API.
func FodmapAllergenTools() []ToolDeclaration {
	return []ToolDeclaration{
		{
			Name:        "lookup_fodmap",
			Description: "Look up the FODMAP classification for a food ingredient. Returns FODMAP groups present and whether the ingredient is high, moderate, or low FODMAP.",
			Parameters:  json.RawMessage(`{"type":"OBJECT","properties":{"ingredient":{"type":"STRING","description":"The food ingredient name to look up (e.g. \"garlic\", \"wheat\", \"milk\")"}},"required":["ingredient"]}`),
		},
		{
			Name:        "lookup_allergens",
			Description: "Look up common allergens for a food ingredient using the Open Food Facts database.",
			Parameters:  json.RawMessage(`{"type":"OBJECT","properties":{"ingredient":{"type":"STRING","description":"The food ingredient name to look up (e.g. \"garlic\", \"wheat\", \"milk\")"}},"required":["ingredient"]}`),
		},
		{
			Name:        "lookup_product_fodmap",
			Description: "Assess the overall FODMAP level of a packaged or branded food product by name. Fetches the product's ingredient list from Open Food Facts, classifies each ingredient, and returns the worst-case FODMAP level plus the groups present.",
			Parameters:  json.RawMessage(`{"type":"OBJECT","properties":{"product":{"type":"STRING","description":"The packaged or branded product name to assess (e.g. \"Oreo cookies\", \"Heinz tomato ketchup\")"}},"required":["product"]}`),
		},
	}
}

// ---- system prompt rendering ----

// PromptData holds the values injected into the chat system prompt template.
type PromptData struct {
	BusinessName   string
	City           string
	State          string
	DietaryProfile string
}

// RenderChatSystemPrompt renders the system prompt template with business and
// dietary profile data.
func RenderChatSystemPrompt(tmplStr string, biz *Business, dietaryProfile string) (string, error) {
	tmpl, err := template.New("chat").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parsing instruction template: %w", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, PromptData{
		BusinessName:   biz.Name,
		City:           biz.City,
		State:          biz.State,
		DietaryProfile: dietaryProfile,
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

// HTTPFodmapServerClient is an HTTP-based implementation of FodmapServerClient
// that calls the FODMAP detector server's search and lookup endpoints.
type HTTPFodmapServerClient struct {
	serverURL string
	client    *http.Client
}

// NewHTTPFodmapServerClient creates an HTTPFodmapServerClient targeting the
// given server base URL.
func NewHTTPFodmapServerClient(serverURL string) *HTTPFodmapServerClient {
	return &HTTPFodmapServerClient{
		serverURL: serverURL,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

// FetchTopBusiness searches for the top business matching the query and
// location filters.
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
	defer func() { _ = resp.Body.Close() }()

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

// FetchChatReviews fetches reviews for a business to use as chat context.
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
	defer func() { _ = resp.Body.Close() }()

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

// LookupFodmap looks up the FODMAP classification for a single ingredient.
func (c *HTTPFodmapServerClient) LookupFodmap(ctx context.Context, ingredient string) (FodmapToolResponse, error) {
	u := c.serverURL + "/api/v1/search/fodmap/" + url.PathEscape(ingredient)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return FodmapToolResponse{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return FodmapToolResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

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
		Level         string   `json:"level"`
		Groups        []string `json:"groups"`
		Notes         string   `json:"notes"`
		Substitutions []string `json:"substitutions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return FodmapToolResponse{}, err
	}

	return FodmapToolResponse{
		Ingredient:    ingredient,
		Found:         true,
		FodmapLevel:   data.Level,
		FodmapGroups:  data.Groups,
		Notes:         data.Notes,
		Substitutions: data.Substitutions,
	}, nil
}

// OpenFoodFactsClient queries the Open Food Facts API for product allergens.
type OpenFoodFactsClient struct {
	baseURL   string
	client    *http.Client
	cache     sync.Map      // allergen lookups keyed by ingredient
	prodCache sync.Map      // ingredient lists keyed by product
	mu        sync.Mutex    // serializes requests to avoid rate-limiting
	lastCall  time.Time     // tracks last request time
	minDelay  time.Duration // minimum delay between requests
}

// throttle serializes Open Food Facts requests and enforces minDelay between
// them to avoid rate-limiting.
func (c *OpenFoodFactsClient) throttle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elapsed := time.Since(c.lastCall); elapsed < c.minDelay {
		time.Sleep(c.minDelay - elapsed)
	}
	c.lastCall = time.Now()
}

// NewOpenFoodFactsClient creates an OpenFoodFactsClient. Pass an empty baseURL
// to use the default public endpoint.
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

// LookupAllergens returns the allergens associated with the given ingredient
// via the Open Food Facts API.
func (c *OpenFoodFactsClient) LookupAllergens(ctx context.Context, ingredient string) (AllergenToolResponse, error) {
	if cached, ok := c.cache.Load(ingredient); ok {
		return cached.(AllergenToolResponse), nil
	}

	c.throttle()
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
	defer func() { _ = resp.Body.Close() }()

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

// LookupProductIngredients searches Open Food Facts for a product by name and
// returns the cleaned ingredient names of the first matching product that lists
// ingredients. Names are normalized — the "en:" locale prefix is removed and
// hyphens become spaces — so they can be passed straight to a FODMAP lookup.
func (c *OpenFoodFactsClient) LookupProductIngredients(ctx context.Context, product string) ([]string, error) {
	if cached, ok := c.prodCache.Load(product); ok {
		return cached.([]string), nil
	}

	c.throttle()
	searchURL := c.baseURL + "?search_terms=" +
		url.QueryEscape(product) +
		"&search_simple=1&action=process&json=1&page_size=3&fields=ingredients_tags"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building OFF request: %w", err)
	}
	req.Header.Set("User-Agent", "fodmap-detector/1.0")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OFF request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("open food facts returned status %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Products []struct {
			IngredientsTags []string `json:"ingredients_tags"`
		} `json:"products"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing Open Food Facts response: %w", err)
	}

	var ingredients []string
	for _, p := range result.Products {
		if len(p.IngredientsTags) == 0 {
			continue
		}
		seen := make(map[string]bool)
		for _, tag := range p.IngredientsTags {
			name := strings.ReplaceAll(strings.TrimPrefix(tag, "en:"), "-", " ")
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			ingredients = append(ingredients, name)
		}
		break
	}

	c.prodCache.Store(product, ingredients)
	return ingredients, nil
}
