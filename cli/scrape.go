package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"fodmap/menusearch"
	"fodmap/pipeline"
	"fodmap/scraper"
	"fodmap/search"
	"fodmap/server"

	"github.com/google/uuid"
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
	scrapeCmd.Flags().String("menu-store", "", "Menu store backend: postgres | weaviate | dual (preferred over --store)")
	scrapeCmd.Flags().String("store", "weaviate", "Storage backend (deprecated alias for --menu-store): weaviate | postgres | pinecone")
	scrapeCmd.Flags().String("weaviate", "localhost:8090", "Weaviate host:port")
	scrapeCmd.Flags().String("weaviate-scheme", "http", "Weaviate scheme (http or https)")
	scrapeCmd.Flags().String("weaviate-api-key", "", "Weaviate API key")
	scrapeCmd.Flags().String("postgres-dsn", "", "PostgreSQL DSN (required if --store=postgres)")
	scrapeCmd.Flags().String("pinecone-api-key", "", "Pinecone API key")
	scrapeCmd.Flags().String("pinecone-index-host", "", "Pinecone index host")

	// Embedding backend
	scrapeCmd.Flags().String("embedder", "ollama", "Embedding backend: ollama | tei | vectorizer")
	scrapeCmd.Flags().String("embed-backend", "ollama", "Embedding backend (deprecated alias for --embedder): ollama | vectorizer")
	scrapeCmd.Flags().String("ollama-url", "http://localhost:11434", "Ollama base URL")
	scrapeCmd.Flags().String("ollama-model", "nomic-embed-text", "Ollama embedding model")
	scrapeCmd.Flags().String("vectorizer", "", "HTTP vectorizer host:port (legacy; prefer --embedder=vectorizer with --vectorizer-url)")
	scrapeCmd.Flags().String("vectorizer-url", "", "HTTP vectorizer base URL (used when --embedder=vectorizer)")
	scrapeCmd.Flags().String("tei-url", "", "Text Embeddings Inference (TEI) service URL (used when --embedder=tei)")
	scrapeCmd.Flags().String("tei-model", "nomic-embed-text", "TEI model name (informational; TEI serves one model per instance)")

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

	// Build embedder. Legacy --vectorizer flag (host:port) maps to the
	// vectorizer backend; --embedder is the canonical selector.
	embedderType := viper.GetString("embedder")
	vectorizerURL := viper.GetString("vectorizer-url")
	if vectorizerHost != "" && embedderType == "ollama" {
		if _, _, err := net.SplitHostPort(vectorizerHost); err != nil {
			return fmt.Errorf("invalid --vectorizer value %q: must be host:port", vectorizerHost)
		}
		embedderType = "vectorizer"
		vectorizerURL = "http://" + vectorizerHost
	}
	embedder, err := search.NewEmbedder(ctx, search.EmbedderConfig{
		Type:          embedderType,
		OllamaURL:     ollamaURL,
		OllamaModel:   ollamaModel,
		TEIURL:        viper.GetString("tei-url"),
		TEIModel:      viper.GetString("tei-model"),
		VectorizerURL: vectorizerURL,
	})
	if err != nil {
		return fmt.Errorf("building embedder: %w", err)
	}
	defer func() { _ = embedder.Close() }()
	slog.Info("embedder ready", "type", embedderType)

	// Build MenuStore. --menu-store selects postgres|weaviate|dual; when
	// empty, the legacy --store flag is consulted for backward compat.
	menuStoreType := viper.GetString("menu-store")
	if menuStoreType == "" {
		menuStoreType = viper.GetString("store")
		// Legacy --store values: weaviate|postgres|pinecone. Map to the new
		// factory (pinecone is unsupported for menus -> error).
		if menuStoreType == "pinecone" {
			return fmt.Errorf("pinecone does not support menus yet; use --menu-store=postgres|weaviate|dual")
		}
	}
	store, err := server.NewMenuStore(ctx, server.MenuStoreConfig{
		Type:           menuStoreType,
		PostgresDSN:    viper.GetString("postgres-dsn"),
		WeaviateHost:   weaviateHost,
		WeaviateScheme: weaviateScheme,
		WeaviateAPIKey: weaviateAPIKey,
		Embedder:       embedder,
	})
	if err != nil {
		return fmt.Errorf("building store: %w", err)
	}

	// Build extractor. When --extractor-url is set, all extraction routes to
	// the Python scraper service (HTML/text, PDF, image). When empty, the plain
	// OpenAICompatExtractor is used for direct LLM extraction.
	var ex scraper.Extractor
	if extractorURL != "" {
		ex = scraper.NewServiceExtractor(extractorURL, pageTimeout, pdfTimeout)
		slog.Info("using scraper service for extraction", "url", extractorURL,
			"page_timeout", pageTimeout, "pdf_timeout", pdfTimeout)
	} else {
		oaex, err := scraper.NewOpenAICompatExtractor(llmURL, llmModel, llmAPIKey, llmReasoningEffort)
		if err != nil {
			return fmt.Errorf("building extractor: %w", err)
		}
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

	result, rawBody, err := pipeline.ExtractMenu(ctx, rawURL, fetcher, ex, enableVision, usePdftotext, webagentAdapter)
	if err != nil {
		return err
	}

	camis := "cli-manual"
	jobID := "0"
	attempt := int(time.Now().Unix())

	if len(rawBody) > 0 {
		date := time.Now().UTC().Format("2006-01-02")
		bronzeDir := os.Getenv("RESTAURANT_BRONZE_DIR")
		if bronzeDir == "" {
			bronzeDir = "data/bronze/restaurants"
		}
		htmlPath := filepath.Join(bronzeDir, date, fmt.Sprintf("%s-%d.html", camis, attempt))
		if mkErr := os.MkdirAll(filepath.Dir(htmlPath), 0o755); mkErr == nil {
			if wErr := os.WriteFile(htmlPath, rawBody, 0o644); wErr != nil {
				slog.Warn("failed to write HTML bronze", "path", htmlPath, "error", wErr)
			} else {
				slog.Info("saved raw body to bronze layer", "path", htmlPath)
			}
		}
	}

	if result == nil || len(result.Items) == 0 {
		fmt.Printf("No menu items found at %s\n", rawURL)
		return nil
	}

	items := make([]search.MenuItem, 0, len(result.Items))
	for _, entry := range result.Items {
		items = append(items, search.MenuItem{
			DishName:           entry.DishName,
			Description:        entry.Description,
			StatedIngredients:  entry.StatedIngredients,
			HasFullIngredients: entry.HasFullIngredients,
		})
	}

	record := menusearch.MenuExtractionRecord{
		CAMIS:            camis,
		SourceURL:        rawURL,
		RestaurantName:   result.RestaurantName,
		Items:            items,
		EventID:          uuid.NewString(),
		JobID:            jobID,
		Attempt:          attempt,
		DiscoveryEventID: "",
	}

	avroDir := os.Getenv("RESTAURANT_AVRO_DIR")
	if avroDir == "" {
		avroDir = "data/silver/menus"
	}
	avroDest := filepath.Join(avroDir, time.Now().UTC().Format("2006-01-02"), fmt.Sprintf("%s-%d.avro", camis, attempt))
	if mkErr := os.MkdirAll(filepath.Dir(avroDest), 0o755); mkErr == nil {
		if err := menusearch.WriteMenuExtractionAvro(ctx, avroDest, record); err != nil {
			slog.Error("failed to write avro", "error", err)
		} else {
			slog.Info("saved extraction record to silver layer", "path", avroDest)
		}
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
