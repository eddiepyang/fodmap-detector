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

type FodmapServerClient interface {
	FetchTopBusiness(ctx context.Context, query, category, city, state string) (*Business, error)
	FetchChatReviews(ctx context.Context, businessID, query string, limit int) ([]Review, error)
	LookupFODMAP(ctx context.Context, ingredient string) (FodmapToolResponse, error)
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
func IsFoodRelated(ctx context.Context, client *genai.Client, input string) (bool, error) {
	prompt := fmt.Sprintf(
		"Is the following message asking about food, restaurants, ingredients, dietary restrictions, allergens, or FODMAP content? Answer with exactly \"yes\" or \"no\".\nMessage: %q",
		input,
	)
	resp, err := client.Models.GenerateContent(ctx, ScreenGeminiModel, genai.Text(prompt), nil)
	if err != nil {
		return true, fmt.Errorf("topic screen: %w", err)
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(resp.Text())), "y"), nil
}

// ---- chat session (tool-call loop) ----

// Session manages tool dispatch for a Gemini chat with FODMAP/allergen tools.
type Session struct {
	FodmapClient   FodmapServerClient
	AllergenClient AllergenClient
}

// SendResult contains the outcome of a chat turn.
type SendResult struct {
	Text      string
	ToolCalls []string
}

// SendWithToolCalls sends a user message and iterates until the model returns a
// plain-text response, dispatching any function calls in between.
// The optional onText callback is invoked for each streamed text chunk (for CLI printing).
func (s *Session) SendWithToolCalls(ctx context.Context, chat *genai.Chat, input string, onText func(string)) (SendResult, error) {
	var parts []genai.Part
	if input != "" {
		parts = []genai.Part{{Text: input}}
	}

	var result SendResult
	var fullText strings.Builder
	for {
		var toolParts []genai.Part
		for resp, err := range chat.SendMessageStream(ctx, parts...) {
			if err != nil {
				return SendResult{}, fmt.Errorf("stream error: %w", err)
			}
			if len(resp.Candidates) == 0 {
				continue
			}
			candidate := resp.Candidates[0]
			if candidate.Content == nil {
				continue
			}
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					fullText.WriteString(part.Text)
					if onText != nil {
						onText(part.Text)
					}
				}
				if part.FunctionCall != nil {
					toolResult := s.DispatchTool(ctx, part.FunctionCall.Name, part.FunctionCall.Args)
					toolParts = append(toolParts,
						*genai.NewPartFromFunctionResponse(part.FunctionCall.Name, ToMap(toolResult)),
					)
					result.ToolCalls = append(result.ToolCalls, fmt.Sprintf("%s(%v)", part.FunctionCall.Name, part.FunctionCall.Args["ingredient"]))
					if onText != nil {
						onText(fmt.Sprintf("\n[Tool Call] %s\n", part.FunctionCall.Name))
					}
				}
			}
		}

		if len(toolParts) > 0 {
			parts = toolParts
			continue
		}
		break
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
	Reviews      string
}

func RenderChatSystemPrompt(tmplStr string, biz *Business, reviews []Review) (string, error) {
	tmpl, err := template.New("chat").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parsing instruction template: %w", err)
	}
	var sb strings.Builder
	for i, r := range reviews {
		fmt.Fprintf(&sb, "--- Review %d (stars: %.1f) ---\n%s\n\n", i+1, r.Stars, r.Text)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, PromptData{
		BusinessName: biz.Name,
		City:         biz.City,
		State:        biz.State,
		Reviews:      sb.String(),
	}); err != nil {
		return "", fmt.Errorf("executing prompt: %w", err)
	}
	return buf.String(), nil
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
	u := c.serverURL + "/searchBusiness/" + url.PathEscape(query) + "?limit=1"
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
	u := c.serverURL + "/searchReview/" + url.PathEscape(query) + "?business_id=" + url.QueryEscape(businessID) + "&limit=" + strconv.Itoa(limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", u, err)
	}
	defer resp.Body.Close()

	var data struct {
		Reviews []Review `json:"reviews"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decoding reviews response: %w", err)
	}
	return data.Reviews, nil
}

func (c *HTTPFodmapServerClient) LookupFODMAP(ctx context.Context, ingredient string) (FodmapToolResponse, error) {
	u := c.serverURL + "/searchFodmap/" + url.PathEscape(ingredient)
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
