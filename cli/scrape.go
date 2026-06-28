package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"fodmap/pipeline"
	"fodmap/scraper"
	"fodmap/search"
	"fodmap/server"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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

	// LLM extraction backend — any OpenAI-compatible endpoint.
	// Default targets vLLM (vllm-metal on Mac, vLLM on the 5080): unlike Ollama's
	// MLX engine, vLLM enforces response_format json_schema, which the extractor
	// relies on. See docs/guides/llm-serving.md.
	// --llm-url must include the version segment:
	//   vLLM:   http://localhost:8000/v1
	//   Ollama: http://localhost:11434/v1 (chat only; does not enforce json_schema)
	//   OpenAI: https://api.openai.com/v1
	//   Gemini: https://generativelanguage.googleapis.com/v1beta/openai
	scrapeCmd.Flags().String("llm-url", "http://localhost:8000/v1", "Base URL for the OpenAI-compatible LLM endpoint (include version segment)")
	scrapeCmd.Flags().String("llm-model", "qwen3-vl", "LLM model name")
	scrapeCmd.Flags().String("llm-api-key", "", "API key for cloud backends (OpenAI, Gemini, etc.)")
	scrapeCmd.Flags().String("llm-reasoning-effort", "none", "Reasoning effort: none | low | medium | high (none = fastest, cost-optimal for Gemini)")

	// Fetch options
	scrapeCmd.Flags().Bool("ignore-robots", false, "Skip robots.txt check")
	scrapeCmd.Flags().Bool("enable-js-render", false, "Route noisy/JS-only HTML pages to the scraper service's webagent (requires --extractor-url + --webagent-adapter)")
	scrapeCmd.Flags().Bool("enable-vision", false, "Send PDFs/images to the vision LLM instead of text extraction (pure-Go fallback; mutually alternative with --extractor-url)")
	scrapeCmd.Flags().Bool("pdftotext", false, "Use system pdftotext (poppler) for PDF text extraction")

	// Scraper service (Phase A): route PDF/OCR extraction to the Python service.
	// When set, PDFs that fail the local text-layer/pdftotext cascade are sent to
	// the service instead of the pure-Go vision path. HTML/text extraction still
	// uses the local --llm-* extractor. PDF structuring is then owned by the
	// service's SCRAPER_LLM_* / OCR backend config — the detector's --llm-model
	// / --llm-url only drive the HTML/text path (embeddings remain on --ollama-*).
	scrapeCmd.Flags().String("extractor-url", "", "Base URL of the Python scraper service for PDF/OCR (e.g. http://localhost:8765); empty = pure-Go default")
	scrapeCmd.Flags().Duration("extractor-page-timeout", 2*time.Minute, "Per-page request timeout when calling the scraper service (OCR VLM is slow)")
	scrapeCmd.Flags().Duration("extractor-pdf-timeout", 10*time.Minute, "Overall PDF deadline when calling the scraper service (multi-page scans can run for minutes)")

	// webagent (Phase B): route JS-rendered pages to the service's webagent
	// endpoint. Requires --extractor-url + a pre-compiled adapter (offline
	// authoring step in the Python repo). --enable-js-render gates this path.
	scrapeCmd.Flags().String("webagent-adapter", "", "webagent adapter ID (site/target) for JS-rendered pages, e.g. 'amc/seats'; requires --enable-js-render + --extractor-url")
}

func runScrape(cmd *cobra.Command, args []string) error {
	rawURL := args[0]
	ctx := context.Background()

	ignoreRobots, _ := cmd.Flags().GetBool("ignore-robots")
	enableVision, _ := cmd.Flags().GetBool("enable-vision")
	enableJSRender, _ := cmd.Flags().GetBool("enable-js-render")
	usePdftotext, _ := cmd.Flags().GetBool("pdftotext")

	llmURL := viper.GetString("llm-url")
	llmModel := viper.GetString("llm-model")
	llmAPIKey := viper.GetString("llm-api-key")
	llmReasoningEffort := viper.GetString("llm-reasoning-effort")

	ollamaURL := viper.GetString("ollama-url")
	ollamaModel := viper.GetString("ollama-model")
	vectorizerHost := viper.GetString("vectorizer")

	weaviateHost := viper.GetString("weaviate")
	weaviateScheme := viper.GetString("weaviate-scheme")
	weaviateAPIKey := viper.GetString("weaviate-api-key")

	extractorURL := viper.GetString("extractor-url")
	pageTimeout := viper.GetDuration("extractor-page-timeout")
	pdfTimeout := viper.GetDuration("extractor-pdf-timeout")
	webagentAdapter := viper.GetString("webagent-adapter")

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

	// Build extractor. When --extractor-url is set, wrap the OpenAI-compat
	// extractor in a ServiceExtractor so PDFs route to the Python service
	// while HTML/text still uses the local LLM. When empty, the plain
	// OpenAICompatExtractor is used (pure-Go default, no behavior change).
	var ex scraper.Extractor
	oaex, err := scraper.NewOpenAICompatExtractor(llmURL, llmModel, llmAPIKey, llmReasoningEffort)
	if err != nil {
		return fmt.Errorf("building extractor: %w", err)
	}
	if extractorURL != "" {
		ex = scraper.NewServiceExtractor(extractorURL, oaex, pageTimeout, pdfTimeout)
		slog.Info("using scraper service for PDF/OCR", "url", extractorURL,
			"page_timeout", pageTimeout, "pdf_timeout", pdfTimeout)
	} else {
		ex = oaex
	}

	var fetcher scraper.Fetcher = scraper.NewHTTPFetcher(ignoreRobots)

	// When --enable-js-render is set without a --webagent-adapter, use the
	// generic render-and-re-cascade path: a headless Chrome fetcher whose
	// FetchRendered lets the cascade re-run text/image extraction on hydrated
	// DOM. With an adapter, the per-site webagent path is used instead (it has
	// selector-level guarantees the generic render lacks), and a plain
	// HTTPFetcher suffices — the webagent owns its own browser.
	if enableJSRender && webagentAdapter == "" {
		cf := scraper.NewChromeRenderedFetcher(ctx, ignoreRobots)
		defer func() {
			cf.Close()
		}()
		fetcher = &forceRenderFetcher{rf: cf}
		slog.Info("using headless Chrome for JS rendering (generic path; no adapter)")
	}

	result, err := pipeline.ExtractMenu(ctx, rawURL, fetcher, ex, enableVision, usePdftotext, webagentAdapter)
	if err != nil {
		return err
	}
	if result == nil || len(result.Items) == 0 {
		fmt.Printf("No menu items found at %s\n", rawURL)
		return nil
	}

	count, err := pipeline.StoreMenu(ctx, result, rawURL, store, embedder)
	if err != nil {
		return err
	}

	fmt.Printf("Scraped %d menu items from %q (%s)\n", count, result.RestaurantName, rawURL)
	return nil
}

type forceRenderFetcher struct {
	rf scraper.RenderedFetcher
}

func (f *forceRenderFetcher) Fetch(ctx context.Context, url string) (scraper.FetchResult, error) {
	return f.rf.FetchRendered(ctx, url)
}

// buildMenuStore constructs a Weaviate-backed MenuStore.
func buildMenuStore(_ context.Context, host, scheme, apiKey string, embedder search.Embedder) (server.MenuStore, error) {
	client, err := search.NewClient(host, scheme, apiKey, embedder)
	if err != nil {
		return nil, fmt.Errorf("weaviate client: %w", err)
	}
	return client, nil
}
