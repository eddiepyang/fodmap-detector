package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/genai"
)

const (
	chatGeminiModel   = "gemini-3.1-flash"
	screenGeminiModel = "gemini-3.1-flash-lite" // lightweight per-turn topic pre-screen
	maxInputLen       = 2000
	maxResponseLen    = 4000
)

// injectionPatterns are case-insensitive substrings that indicate a prompt injection attempt.
var injectionPatterns = []string{
	"ignore previous instructions",
	"ignore all previous",
	"forget your instructions",
	"disregard your instructions",
	"you are now",
	"pretend you are",
	"<|system|>",
	"<|im_start|>",
}

var chatCmd = &cobra.Command{
	Use:   "chat <query>",
	Short: "Start an interactive FODMAP/allergen chat for a restaurant query.",
	Args:  cobra.ExactArgs(1),
	RunE:  runChat,
}

func init() {
	rootCmd.AddCommand(chatCmd)
	chatCmd.Flags().String("server", "http://localhost:8080", "Base URL of the fodmap server")
	chatCmd.Flags().Int("limit", 20, "Max reviews to include in context")
	chatCmd.Flags().String("prompt", "./chat-prompt.txt", "Path to the chat system prompt template")
	chatCmd.Flags().String("category", "", "Filter businesses by category substring")
	chatCmd.Flags().String("city", "", "Filter businesses by city (exact match)")
	chatCmd.Flags().String("state", "", "Filter businesses by state (exact match)")
	chatCmd.Flags().String("model", chatGeminiModel, "Gemini model ID for the chat session")
}

// ---- domain types for HTTP responses ----

type chatBusiness struct {
	ID    string
	Name  string
	City  string
	State string
}

// chatReview mirrors schemas.Review JSON output. Stars and Text have no json tags
// on the source struct so they serialize under their Go field names.
type chatReview struct {
	ReviewID string  `json:"review_id"`
	Stars    float32 `json:"Stars"`
	Text     string  `json:"Text"`
}

// ---- command entry point ----

func runChat(cmd *cobra.Command, args []string) error {
	query := args[0]
	serverURL, _ := cmd.Flags().GetString("server")
	limit, _ := cmd.Flags().GetInt("limit")
	promptPath, _ := cmd.Flags().GetString("prompt")
	category, _ := cmd.Flags().GetString("category")
	city, _ := cmd.Flags().GetString("city")
	state, _ := cmd.Flags().GetString("state")
	model, _ := cmd.Flags().GetString("model")

	ctx := context.Background()

	biz, err := fetchTopBusiness(ctx, serverURL, query, category, city, state)
	if err != nil {
		return fmt.Errorf("searching businesses: %w", err)
	}
	fmt.Printf("Found: %s (%s, %s)\n", biz.Name, biz.City, biz.State)

	reviews, err := fetchChatReviews(ctx, serverURL, biz.ID, limit)
	if err != nil {
		return fmt.Errorf("fetching reviews: %w", err)
	}
	fmt.Printf("Fetched %d reviews. Starting chat (type 'exit' to quit)...\n", len(reviews))

	systemPrompt, err := renderChatSystemPrompt(promptPath, biz, reviews)
	if err != nil {
		return fmt.Errorf("rendering system prompt: %w", err)
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("GEMINI_API_KEY environment variable is not set")
	}
	geminiClient, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return fmt.Errorf("creating Gemini client: %w", err)
	}

	chat, err := geminiClient.Chats.Create(ctx, model, &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemPrompt}},
		},
		Tools: []*genai.Tool{fodmapAllergenTools()},
	}, nil)
	if err != nil {
		return fmt.Errorf("creating chat session: %w", err)
	}

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			fmt.Print("> ")
			continue
		}
		if input == "exit" || input == "quit" {
			break
		}

		// Code-level guardrails.
		if err := validateChatInput(input); err != nil {
			fmt.Printf("[blocked] %v\n> ", err)
			continue
		}

		// Per-turn topic pre-screen.
		if foodRelated, err := isFoodRelated(ctx, geminiClient, input); err != nil {
			slog.Warn("topic screen error", "error", err)
		} else if !foodRelated {
			fmt.Print("Sorry, I can only help with food, ingredients, FODMAP, and allergen questions.\n> ")
			continue
		}

		response, err := sendWithToolCalls(ctx, chat, input)
		if err != nil {
			fmt.Printf("[error] %v\n> ", err)
			continue
		}
		if len(response) > maxResponseLen {
			slog.Warn("long model response", "length", len(response))
		}
		fmt.Printf("%s\n> ", response)
	}
	return scanner.Err()
}

// ---- guardrails ----

func validateChatInput(input string) error {
	if len(input) > maxInputLen {
		return fmt.Errorf("message too long (max %d characters)", maxInputLen)
	}
	lower := strings.ToLower(input)
	for _, p := range injectionPatterns {
		if strings.Contains(lower, p) {
			return fmt.Errorf("message contains disallowed content")
		}
	}
	return nil
}

// isFoodRelated runs a lightweight single-turn Gemini call to check whether the
// user's message is on-topic. Fails open on error to avoid blocking valid queries.
func isFoodRelated(ctx context.Context, client *genai.Client, input string) (bool, error) {
	prompt := fmt.Sprintf(
		"Is the following message asking about food, restaurants, ingredients, dietary restrictions, allergens, or FODMAP content? Answer with exactly \"yes\" or \"no\".\nMessage: %q",
		input,
	)
	resp, err := client.Models.GenerateContent(ctx, screenGeminiModel, genai.Text(prompt), nil)
	if err != nil {
		return true, fmt.Errorf("topic screen: %w", err)
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(resp.Text())), "y"), nil
}

// ---- tool-call loop ----

// sendWithToolCalls sends a user message and iterates until the model returns a
// plain-text response, dispatching any function calls in between.
func sendWithToolCalls(ctx context.Context, chat *genai.Chat, input string) (string, error) {
	resp, err := chat.SendMessage(ctx, genai.Part{Text: input})
	if err != nil {
		return "", fmt.Errorf("send message: %w", err)
	}
	for {
		var toolParts []genai.Part
		for _, candidate := range resp.Candidates {
			if candidate.Content == nil {
				continue
			}
			for _, part := range candidate.Content.Parts {
				if part.FunctionCall == nil {
					continue
				}
				result := dispatchTool(ctx, part.FunctionCall.Name, part.FunctionCall.Args)
				toolParts = append(toolParts,
					*genai.NewPartFromFunctionResponse(part.FunctionCall.Name, result),
				)
			}
		}
		if len(toolParts) == 0 {
			break
		}
		resp, err = chat.SendMessage(ctx, toolParts...)
		if err != nil {
			return "", fmt.Errorf("send tool response: %w", err)
		}
	}
	return resp.Text(), nil
}

func dispatchTool(ctx context.Context, name string, args map[string]any) map[string]any {
	ingredient, _ := args["ingredient"].(string)
	switch name {
	case "lookup_fodmap":
		return lookupFODMAP(ingredient)
	case "lookup_allergens":
		result, err := lookupAllergens(ctx, ingredient)
		if err != nil {
			slog.Warn("allergen lookup failed", "ingredient", ingredient, "error", err)
			return map[string]any{"error": err.Error()}
		}
		return result
	default:
		return map[string]any{"error": "unknown tool: " + name}
	}
}

// ---- tool declarations ----

func fodmapAllergenTools() *genai.Tool {
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

// ---- HTTP helpers ----

func fetchTopBusiness(ctx context.Context, serverURL, query, category, city, state string) (*chatBusiness, error) {
	u := serverURL + "/searchBusiness/" + url.PathEscape(query) + "?limit=1"
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
	resp, err := http.DefaultClient.Do(req)
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
	return &chatBusiness{ID: b.ID, Name: b.Name, City: b.City, State: b.State}, nil
}

func fetchChatReviews(ctx context.Context, serverURL, businessID string, limit int) ([]chatReview, error) {
	u := serverURL + "/reviews?business_id=" + url.QueryEscape(businessID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", u, err)
	}
	defer resp.Body.Close()

	var reviews []chatReview
	if err := json.NewDecoder(resp.Body).Decode(&reviews); err != nil {
		return nil, fmt.Errorf("decoding reviews response: %w", err)
	}
	if limit < len(reviews) {
		reviews = reviews[:limit]
	}
	return reviews, nil
}

// ---- system prompt rendering ----

type chatPromptData struct {
	BusinessName string
	City         string
	State        string
	Reviews      string
}

func renderChatSystemPrompt(promptPath string, biz *chatBusiness, reviews []chatReview) (string, error) {
	tmplBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("reading prompt %q: %w", promptPath, err)
	}
	tmpl, err := template.New("chat").Parse(string(tmplBytes))
	if err != nil {
		return "", fmt.Errorf("parsing prompt: %w", err)
	}
	var sb strings.Builder
	for i, r := range reviews {
		fmt.Fprintf(&sb, "--- Review %d (stars: %.1f) ---\n%s\n\n", i+1, r.Stars, r.Text)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, chatPromptData{
		BusinessName: biz.Name,
		City:         biz.City,
		State:        biz.State,
		Reviews:      sb.String(),
	}); err != nil {
		return "", fmt.Errorf("executing prompt: %w", err)
	}
	return buf.String(), nil
}

// ---- allergen lookup via Open Food Facts ----

var offClient = &http.Client{Timeout: 10 * time.Second}

// offBaseURL is the Open Food Facts search endpoint. Overridden in tests.
var offBaseURL = "https://world.openfoodfacts.org/cgi/search.pl"

func lookupAllergens(ctx context.Context, ingredient string) (map[string]any, error) {
	searchURL := offBaseURL + "?search_terms=" +
		url.QueryEscape(ingredient) +
		"&search_simple=1&action=process&json=1&page_size=3"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building OFF request: %w", err)
	}
	req.Header.Set("User-Agent", "fodmap-detector/1.0")
	resp, err := offClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OFF request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Products []struct {
			AllergensTags []string `json:"allergens_tags"`
		} `json:"products"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decoding OFF response: %w", err)
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
	return map[string]any{
		"ingredient": ingredient,
		"allergens":  allergens,
		"source":     "Open Food Facts",
	}, nil
}
