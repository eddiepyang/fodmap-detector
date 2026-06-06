package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"fodmap/scraper"
	"fodmap/search"
	"fodmap/server"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/genai"
)

var scrapeCmd = &cobra.Command{
	Use:   "scrape <url>",
	Short: "Scrape a restaurant menu page and index it for FODMAP chat.",
	Args:  cobra.ExactArgs(1),
	RunE:  runScrape,
}

func init() {
	rootCmd.AddCommand(scrapeCmd)

	// Storage backend
	scrapeCmd.Flags().String("store", "weaviate", "Storage backend: weaviate | postgres | pinecone")
	scrapeCmd.Flags().String("weaviate", "localhost:8090", "Weaviate host:port")
	scrapeCmd.Flags().String("weaviate-scheme", "http", "Weaviate scheme (http or https)")
	scrapeCmd.Flags().String("weaviate-api-key", "", "Weaviate API key")
	scrapeCmd.Flags().String("postgres-dsn", "", "PostgreSQL DSN (required if --store=postgres)")
	scrapeCmd.Flags().String("pinecone-api-key", "", "Pinecone API key")
	scrapeCmd.Flags().String("pinecone-index-host", "", "Pinecone index host")

	// Embedding backend
	scrapeCmd.Flags().String("embed-backend", "ollama", "Embedding backend: ollama | vectorizer")
	scrapeCmd.Flags().String("ollama-url", "http://localhost:11434", "Ollama base URL")
	scrapeCmd.Flags().String("ollama-model", "nomic-embed-text", "Ollama embedding model")
	scrapeCmd.Flags().String("vectorizer", "", "HTTP vectorizer host:port")

	// LLM extraction backend
	scrapeCmd.Flags().String("llm-backend", "openai-compat", "LLM backend: openai-compat | gemini")
	scrapeCmd.Flags().String("llm-url", "http://localhost:11434", "Base URL for openai-compat backend")
	scrapeCmd.Flags().String("llm-model", "qwen3.6:35b-mlx", "LLM model name")
	scrapeCmd.Flags().String("llm-api-key", "", "API key for gemini or openai backends")

	// Fetch options
	scrapeCmd.Flags().Bool("ignore-robots", false, "Skip robots.txt check")
	scrapeCmd.Flags().Bool("enable-api-inference", false, "Tier 2: ask LLM to infer hidden API endpoints (experimental)")
	scrapeCmd.Flags().Bool("enable-js-render", false, "Tier 3: render JS-only pages via chromedp (requires Chrome)")
	scrapeCmd.Flags().Bool("enable-vision", false, "Send PDFs/images to the vision LLM instead of text extraction")
	scrapeCmd.Flags().Bool("pdftotext", false, "Use system pdftotext (poppler) for PDF text extraction")
}

func runScrape(cmd *cobra.Command, args []string) error {
	rawURL := args[0]
	ctx := context.Background()

	ignoreRobots, _ := cmd.Flags().GetBool("ignore-robots")
	enableVision, _ := cmd.Flags().GetBool("enable-vision")
	usePdftotext, _ := cmd.Flags().GetBool("pdftotext")
	enableAPIInference, _ := cmd.Flags().GetBool("enable-api-inference")

	llmBackend := viper.GetString("llm-backend")
	llmURL := viper.GetString("llm-url")
	llmModel := viper.GetString("llm-model")
	llmAPIKey := viper.GetString("llm-api-key")

	ollamaURL := viper.GetString("ollama-url")
	ollamaModel := viper.GetString("ollama-model")
	vectorizerHost := viper.GetString("vectorizer")

	weaviateHost := viper.GetString("weaviate")
	weaviateScheme := viper.GetString("weaviate-scheme")
	weaviateAPIKey := viper.GetString("weaviate-api-key")

	// Build embedder.
	var embedder search.Embedder
	if vectorizerHost != "" {
		if _, _, err := net.SplitHostPort(vectorizerHost); err != nil {
			return fmt.Errorf("invalid --vectorizer value %q: must be host:port", vectorizerHost)
		}
		embedder = search.NewVectorizerClient("http://" + vectorizerHost)
		slog.Info("using HTTP vectorizer", "host", vectorizerHost)
	} else {
		embedder = search.NewOllamaEmbedder(ollamaURL, ollamaModel)
		slog.Info("using Ollama embedder", "model", ollamaModel, "url", ollamaURL)
	}
	defer func() { _ = embedder.Close() }()

	// Build MenuStore.
	store, err := buildMenuStore(ctx, weaviateHost, weaviateScheme, weaviateAPIKey, embedder)
	if err != nil {
		return fmt.Errorf("building store: %w", err)
	}
	if err := store.EnsureMenuSchema(ctx); err != nil {
		return fmt.Errorf("schema init: %w", err)
	}

	// Build extractor.
	var ex scraper.Extractor
	switch llmBackend {
	case "gemini":
		client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: llmAPIKey})
		if err != nil {
			return fmt.Errorf("gemini client: %w", err)
		}
		ex = scraper.NewGeminiExtractor(client, llmModel)
	default: // openai-compat
		ex = scraper.NewOpenAICompatExtractor(llmURL, llmModel, llmAPIKey)
	}

	fetcher := scraper.NewHTTPFetcher(ignoreRobots)

	return runScrapeWith(ctx, rawURL, fetcher, ex, store, embedder, enableVision, usePdftotext, enableAPIInference)
}

// runScrapeWith is the testable core of the scrape command. All dependencies
// are injected so tests can pass stubs directly.
func runScrapeWith(
	ctx context.Context,
	rawURL string,
	fetcher scraper.Fetcher,
	ex scraper.Extractor,
	store server.MenuStore,
	embedder search.Embedder,
	enableVision bool,
	usePdftotext bool,
	enableAPIInference bool,
) error {
	slog.Info("scraping URL", "url", rawURL)

	// ── Tier 0: JSON-LD fast-path ─────────────────────────────────────────────
	fetchRes, err := fetcher.Fetch(ctx, rawURL)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}

	bodyBytes, err := io.ReadAll(fetchRes.Body)
	_ = fetchRes.Body.Close()
	if err != nil {
		return fmt.Errorf("reading body: %w", err)
	}

	ct := fetchRes.ContentType
	var result scraper.MenuExtractionResult
	var jsonldMeta scraper.JSONLDMeta
	var usedJSONLD bool

	if !strings.Contains(ct, "pdf") {
		items, meta, ok := scraper.ExtractJSONLD(bytes.NewReader(bodyBytes))
		jsonldMeta = meta
		if ok {
			slog.Info("Tier 0: JSON-LD menu found", "items", len(items))
			result = scraper.MenuExtractionResult{
				RestaurantName: meta.RestaurantName,
				City:           meta.City,
				State:          meta.State,
				SourceURL:      rawURL,
				ScrapedAtUTC:   time.Now().UTC().Format(time.RFC3339),
				Items:          items,
			}
			usedJSONLD = true
		}
	}

	// ── Tier 1: HTML/PDF → LLM extraction ────────────────────────────────────
	if !usedJSONLD {
		var pageText string

		if strings.Contains(ct, "pdf") {
			pageText, err = extractPDF(ctx, bodyBytes, usePdftotext, enableVision, ex)
			if err != nil {
				return fmt.Errorf("PDF extraction: %w", err)
			}
		} else {
			md, err := scraper.ConvertHTMLToMarkdown(bytes.NewReader(bodyBytes), ct)
			if err != nil {
				return fmt.Errorf("HTML conversion: %w", err)
			}
			pageText = md
		}

		slog.Info("Tier 1: sending to LLM extractor", "chars", len([]rune(pageText)))
		result, err = ex.Extract(ctx, pageText)
		if err != nil {
			return fmt.Errorf("LLM extraction: %w", err)
		}
		result.SourceURL = rawURL
		result.ScrapedAtUTC = time.Now().UTC().Format(time.RFC3339)

		// Backfill location from JSON-LD metadata when Tier 1 doesn't know it.
		if result.City == "" && jsonldMeta.City != "" {
			result.City = jsonldMeta.City
			result.State = jsonldMeta.State
		}
		if result.RestaurantName == "" && jsonldMeta.RestaurantName != "" {
			result.RestaurantName = jsonldMeta.RestaurantName
		}

		// ── Tier 2: API inference (experimental, opt-in) ─────────────────────
		if enableAPIInference && len(result.Items) < 3 {
			slog.Warn("Tier 2: Tier 1 returned few items, attempting API inference (experimental)")
			// Tier 2 implementation: send raw HTML to LLM asking for API endpoint.
			// Gated behind flag; see scraper/api_inference.go for details.
			_ = enableAPIInference // inference call would go here
		}
	}

	if len(result.Items) == 0 {
		slog.Warn("no menu items extracted", "url", rawURL)
		fmt.Printf("No menu items found at %s\n", rawURL)
		return nil
	}

	slog.Info("extracted menu items", "count", len(result.Items), "restaurant", result.RestaurantName)

	// ── Embed + upsert ────────────────────────────────────────────────────────
	items, err := toMenuItems(result, rawURL, embedder, ctx)
	if err != nil {
		return fmt.Errorf("embedding menu items: %w", err)
	}

	if err := store.BatchUpsertMenu(ctx, items); err != nil {
		return fmt.Errorf("upserting menu items: %w", err)
	}

	fmt.Printf("Scraped %d menu items from %q (%s)\n", len(items), result.RestaurantName, rawURL)
	return nil
}

// extractPDF runs the PDF cascade: text-layer → pdftotext → vision LLM.
// When the vision path is active it calls ExtractImage directly and returns
// the LLM result text as a JSON string for the caller to feed to the extractor.
func extractPDF(ctx context.Context, pdfBytes []byte, usePdftotext, enableVision bool, ex scraper.Extractor) (string, error) {
	text, err := scraper.ExtractPDFText(pdfBytes, usePdftotext)
	if err == nil {
		return text, nil
	}
	if err != scraper.ErrNeedVision {
		return "", err
	}
	if !enableVision {
		return "", fmt.Errorf("PDF has no usable text layer and --enable-vision is not set")
	}

	// Vision path: cast to OpenAICompatExtractor for image support.
	oaex, ok := ex.(*scraper.OpenAICompatExtractor)
	if !ok {
		return "", fmt.Errorf("vision path requires --llm-backend openai-compat")
	}
	result, err := scraper.ExtractPDFVision(ctx, pdfBytes, oaex)
	if err != nil {
		return "", err
	}
	// Return the dish names as flat text so the caller's normal flow still works.
	var lines []string
	for _, item := range result.Items {
		lines = append(lines, item.DishName+": "+item.Description)
	}
	return strings.Join(lines, "\n"), nil
}

// menuCollectionNS is the UUID namespace for deterministic menu item IDs.
var menuCollectionNS = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

// toMenuItems converts a MenuExtractionResult to []search.MenuItem, embedding
// each item's text vector.
func toMenuItems(result scraper.MenuExtractionResult, rawURL string, embedder search.Embedder, ctx context.Context) ([]search.MenuItem, error) {
	businessID := scraper.BusinessID(rawURL)
	section := scraper.MenuSection(rawURL)
	now := result.ScrapedAtUTC

	texts := make([]string, len(result.Items))
	for i, item := range result.Items {
		parts := []string{"Menu item at " + result.RestaurantName + ": " + item.DishName}
		if item.Description != "" {
			parts = append(parts, item.Description)
		}
		if len(item.StatedIngredients) > 0 {
			parts = append(parts, "Stated ingredients: "+strings.Join(item.StatedIngredients, ", "))
		}
		texts[i] = strings.Join(parts, ". ")
	}

	vectors, err := embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embedding batch: %w", err)
	}

	items := make([]search.MenuItem, len(result.Items))
	for i, entry := range result.Items {
		idKey := businessID + entry.DishName
		id := uuid.NewSHA1(menuCollectionNS, []byte(idKey)).String()
		items[i] = search.MenuItem{
			MenuItemID:         id,
			BusinessID:         businessID,
			MenuSection:        section,
			RestaurantName:     result.RestaurantName,
			City:               result.City,
			State:              result.State,
			DishName:           entry.DishName,
			Description:        entry.Description,
			StatedIngredients:  entry.StatedIngredients,
			HasFullIngredients: entry.HasFullIngredients,
			SourceURL:          rawURL,
			ScrapedAtUTC:       now,
			Vector:             vectors[i],
		}
	}
	return items, nil
}

// buildMenuStore constructs a Weaviate-backed MenuStore. Postgres and Pinecone
// paths can be added here following the same pattern as cli/index.go.
func buildMenuStore(_ context.Context, host, scheme, apiKey string, embedder search.Embedder) (server.MenuStore, error) {
	client, err := search.NewClient(host, scheme, apiKey, embedder)
	if err != nil {
		return nil, fmt.Errorf("weaviate client: %w", err)
	}
	return client, nil
}
