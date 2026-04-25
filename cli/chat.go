package cli

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"fodmap/chat"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/genai"
)

// chatGeminiModel is unused or replaced by server config.
const (
	_ = iota
)

var chatCmd = &cobra.Command{
	Use:   "chat <query>",
	Short: "Start an interactive FODMAP/allergen chat for a restaurant query.",
	Args:  cobra.ExactArgs(1),
	RunE:  runChat,
}

func init() {
	rootCmd.AddCommand(chatCmd)
	chatCmd.Flags().String("server", "http://localhost:8081", "Base URL of the fodmap server")
	chatCmd.Flags().Int("limit", 5, "Max reviews to include in context")
	chatCmd.Flags().String("instruction", "", "Optional path to a custom chat instruction template file (overrides the embedded default)")
	chatCmd.Flags().String("category", "", "Filter businesses by category substring")
	chatCmd.Flags().String("city", "", "Filter businesses by city (exact match)")
	chatCmd.Flags().String("state", "", "Filter businesses by state (exact match)")
	chatCmd.Flags().String("chat-model", "gemini-3-flash-preview", "Gemini model ID for the chat session")
	chatCmd.Flags().String("filter-model", "gemini-3.1-flash-lite-preview", "Gemini model ID for topic filtering")
}

func runChat(cmd *cobra.Command, args []string) error {
	query := args[0]
	serverURL := viper.GetString("server")
	limit := viper.GetInt("limit")
	instructionPath := viper.GetString("instruction")
	category := viper.GetString("category")
	city := viper.GetString("city")
	state := viper.GetString("state")
	chatModel := viper.GetString("chat-model")
	filterModel := viper.GetString("filter-model")

	if serverURL == "" {
		return fmt.Errorf("server URL cannot be empty")
	}

	ctx := context.Background()

	fodmapClient := chat.NewHTTPFodmapServerClient(serverURL)
	allergenClient := chat.NewOpenFoodFactsClient("")

	biz, err := fodmapClient.FetchTopBusiness(ctx, query, category, city, state)
	if err != nil {
		return fmt.Errorf("searching businesses: %w", err)
	}
	fmt.Printf("Found: %s (%s, %s)\n", biz.Name, biz.City, biz.State)

	reviews, err := fodmapClient.FetchChatReviews(ctx, biz.ID, query, limit)
	if err != nil {
		return fmt.Errorf("fetching reviews: %w", err)
	}
	fmt.Printf("Fetched %d reviews. Starting chat (type 'exit' to quit)...\n", len(reviews))

	tmplStr := chat.DefaultChatInstruction
	if instructionPath != "" {
		b, err := os.ReadFile(instructionPath)
		if err != nil {
			return fmt.Errorf("reading custom instruction file: %w", err)
		}
		tmplStr = string(b)
	}

	systemPrompt, err := chat.RenderChatSystemPrompt(tmplStr, biz, "")
	if err != nil {
		return fmt.Errorf("rendering system prompt: %w", err)
	}

	var history []*genai.Content
	if len(reviews) > 0 {
		contextContent := chat.FormatReviewsContext(biz.Name, reviews)
		history = append(history, &genai.Content{
			Role:  "model",
			Parts: []*genai.Part{{Text: contextContent}},
		})
	}

	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("GOOGLE_API_KEY environment variable is not set")
	}
	geminiClient, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return fmt.Errorf("creating Gemini client: %w", err)
	}

	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemPrompt}},
		},
		Tools: []*genai.Tool{chat.FodmapAllergenTools()},
	}

	session := &chat.Session{
		FodmapClient:   fodmapClient,
		AllergenClient: allergenClient,
		Model:          chatModel,
		History:        history,
		Config:         config,
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

		if err := chat.ValidateChatInput(input); err != nil {
			fmt.Printf("[blocked] %v\n> ", err)
			continue
		}

		firstMessage := len(session.History) == 0
		if foodRelated, err := chat.IsFoodRelated(ctx, geminiClient, filterModel, input, !firstMessage); err != nil {
			slog.Warn("topic screen error", "error", err)
		} else if !foodRelated {
			fmt.Print("Sorry, I can only help with food, ingredients, FODMAP, and allergen questions.\n> ")
			continue
		}

		result, err := session.SendWithToolCalls(ctx, geminiClient, input, func(text string) {
			fmt.Print(text)
		})
		if err != nil {
			fmt.Printf("[error] %v\n> ", err)
			continue
		}
		if len(result.Text) > chat.MaxResponseLen {
			slog.Warn("long model response", "length", len(result.Text))
		}
		fmt.Print("\n> ")
	}
	return scanner.Err()
}
