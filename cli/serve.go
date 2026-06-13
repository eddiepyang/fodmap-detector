package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"fodmap/auth"
	"fodmap/chat"
	"fodmap/fodmap/store"
	"fodmap/menutracking"
	"fodmap/scraper"
	"fodmap/search"
	"fodmap/server"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP analysis server.",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Use viper for initial values, then override if flags were explicitly set.
		port := viper.GetInt("port")

		weaviateHost := viper.GetString("weaviate")
		if cmd.Flags().Changed("weaviate") {
			weaviateHost, _ = cmd.Flags().GetString("weaviate")
		}

		chatAPIKey := viper.GetString("chat-api-key")
		chatModel := viper.GetString("chat-model")
		filterModel := viper.GetString("filter-model")

		corsOrigins := viper.GetStringSlice("cors-origins")
		if cmd.Flags().Changed("cors-origins") {
			corsOrigins, _ = cmd.Flags().GetStringSlice("cors-origins")
		}
		if len(corsOrigins) == 0 {
			corsOrigins = []string{"http://localhost:5173", "http://localhost:3000"}
		}

		postgresDSN := viper.GetString("postgres-dsn")
		jwtSecret := viper.GetString("jwt-secret")
		weaviateScheme := viper.GetString("weaviate-scheme")
		weaviateAPIKey := viper.GetString("weaviate-api-key")
		pineconeAPIKey := viper.GetString("pinecone-api-key")
		pineconeIndexHost := viper.GetString("pinecone-index-host")
		vectorizerURL := viper.GetString("vectorizer-url")
		ollamaURL := viper.GetString("ollama-url")
		ollamaModel := viper.GetString("ollama-model")
		postgresSearch := viper.GetBool("postgres-search")
		enablePipeline := viper.GetBool("enable-pipeline")

		var embedder search.Embedder
		if ollamaURL != "" && ollamaModel != "" {
			embedder = search.NewOllamaEmbedder(ollamaURL, ollamaModel)
			defer func() { _ = embedder.Close() }()
			slog.Info("using Ollama embedder", "model", ollamaModel, "url", ollamaURL)
		} else if vectorizerURL != "" {
			embedder = search.NewVectorizerClient(vectorizerURL)
			slog.Info("using HTTP vectorizer", "url", vectorizerURL)
		}
		if jwtSecret == "" {
			slog.Warn("JWT_SECRET not set; using insecure default — do not use in production")
			jwtSecret = "change-me-in-production"
		}

		if postgresDSN == "" {
			return fmt.Errorf("postgres-dsn is required")
		}

		var userStore auth.AdminStore
		var err error
		userStore, err = auth.NewPostgresStore(context.Background(), postgresDSN)
		if err != nil {
			return fmt.Errorf("initializing user store: %w", err)
		}
		defer func() { _ = userStore.Close() }()

		catalogStore, err := store.NewFodmapCatalogStore(postgresDSN)
		if err != nil {
			return fmt.Errorf("initializing fodmap catalog store: %w", err)
		}
		defer func() { _ = catalogStore.Close() }()
		if err := catalogStore.EnsureSchema(context.Background()); err != nil {
			return fmt.Errorf("ensuring fodmap catalog schema: %w", err)
		}

		adminEmail := viper.GetString("admin-email")
		if adminEmail != "" {
			user, err := userStore.GetUserByEmail(context.Background(), adminEmail)
			if err != nil {
				slog.Warn("failed to fetch bootstrap admin user on startup", "email", adminEmail, "error", err)
			} else if user != nil {
				if err := userStore.SetUserRole(context.Background(), user.ID, "admin"); err != nil {
					slog.Warn("failed to promote user to admin on startup", "email", adminEmail, "error", err)
				} else {
					slog.Info("promoted user to admin on startup", "email", adminEmail)
				}
			} else {
				slog.Info("admin-email not registered yet, will promote on registration", "email", adminEmail)
			}
		}

		if port <= 0 || port > 65535 {
			return fmt.Errorf("invalid port: %d (must be between 1 and 65535)", port)
		}
		if chatAPIKey != "" && chatModel == "" {
			return fmt.Errorf("chat-model cannot be empty when chat-api-key is provided")
		}

		srv, err := server.New(context.Background(), server.Config{
			Port:               port,
			WeaviateHost:       weaviateHost,
			WeaviateScheme:     weaviateScheme,
			WeaviateAPIKey:     weaviateAPIKey,
			PostgresSearch:     postgresSearch,
			PostgresDSN:        postgresDSN,
			CatalogStore:       catalogStore,
			GeminiAPIKey:       os.Getenv("GOOGLE_API_KEY"),
			ChatModel:          chatModel,
			FilterModel:        filterModel,
			ChatAPIKey:         chatAPIKey,
			CORSAllowedOrigins: corsOrigins,
			UserStore:          userStore,
			JWTSecret:          jwtSecret,
			AdminEmail:         adminEmail,
			PineconeAPIKey:     pineconeAPIKey,
			PineconeIndexHost:  pineconeIndexHost,
			VectorizerURL:      vectorizerURL,
			Embedder:           embedder,
		})
		if err != nil {
			return fmt.Errorf("initializing server: %w", err)
		}

		// Start the menutracking pipeline if enabled. The pipeline shares the
		// server's lifecycle: when srv.Start() returns (on SIGTERM or error),
		// we drain the pipeline before exiting.
		var pipelineResult *PipelineResult
		if enablePipeline {
			if postgresDSN == "" {
				return fmt.Errorf("postgres-dsn is required when --enable-pipeline is set")
			}
			fetcher := scraper.NewHTTPFetcher(true) // ignore robots for regulatory sources

			// Build a VectorSink from the server's Searcher if available.
			var vectorSink menutracking.VectorSink
			if vs, ok := srv.Searcher().(menutracking.VectorSink); ok {
				vectorSink = vs
			}

			// Build a ChatBackend from the server's backend if available.
			var chatBackend chat.ChatBackend
			if cb := srv.ChatBackend(); cb != nil {
				chatBackend = cb
			}

			var pipelineErr error
			pipelineResult, pipelineErr = StartMenutrackingPipeline(context.Background(), PipelineConfig{
				DSN:         postgresDSN,
				Fetcher:     fetcher,
				VectorSink:  vectorSink,
				ChatBackend: chatBackend,
			})
			if pipelineErr != nil {
				return fmt.Errorf("starting menutracking pipeline: %w", pipelineErr)
			}

			// Wire menutracking admin endpoints using the pipeline's pool.
			srv.SetMenutrackingAdmin(&menutracking.MenutrackingAdminHandler{Pool: pipelineResult.Pool})
		}

		// Start the HTTP server (blocks until SIGTERM or error).
		if err := srv.Start(); err != nil {
			if pipelineResult != nil {
				stopCtx, cancel := context.WithTimeout(context.Background(), 30)
				defer cancel()
				_ = pipelineResult.Stop(stopCtx)
			}
			return fmt.Errorf("server error: %w", err)
		}

		// Drain the pipeline on server shutdown.
		if pipelineResult != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 30)
			defer cancel()
			if err := pipelineResult.Stop(stopCtx); err != nil {
				slog.Error("menutracking pipeline shutdown error", "err", err)
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().IntP("port", "p", 8081, "Port to listen on")
	serveCmd.Flags().String("weaviate", "", "Weaviate host:port (e.g. localhost:8090); omit to disable search")
	serveCmd.Flags().String("weaviate-scheme", "http", "Weaviate scheme (http or https)")
	serveCmd.Flags().String("weaviate-api-key", "", "Weaviate API Key (for Weaviate Cloud)")
	serveCmd.Flags().String("chat-api-key", "", "Bearer token for /chat endpoint; omit to disable chat")
	serveCmd.Flags().String("chat-model", "gemini-3-flash-preview", "Gemini model ID for chat sessions")
	serveCmd.Flags().String("filter-model", "gemini-3.1-flash-lite-preview", "Gemini model ID for topic filtering")
	serveCmd.Flags().StringSlice("cors-origins", []string{"http://localhost:3000", "https://app.example.com"}, "Comma-separated list of allowed CORS origins")
	serveCmd.Flags().String("postgres-dsn", "", "PostgreSQL connection string (required)")
	serveCmd.Flags().String("admin-email", "", "Email of the user to promote to admin on startup")
	serveCmd.Flags().Bool("postgres-search", false, "Use PostgreSQL (pgvector) for vector search instead of Weaviate/Pinecone")
	serveCmd.Flags().String("jwt-secret", "", "Secret key for JWT signing (or use JWT_SECRET env var)")
	serveCmd.Flags().String("pinecone-api-key", "", "Pinecone API Key")
	serveCmd.Flags().String("pinecone-index-host", "", "Pinecone Index Host (e.g. https://index-name.svc.pinecone.io)")
	serveCmd.Flags().String("vectorizer-url", "", "Base URL for the HTTP vectorizer-proxy")
	serveCmd.Flags().String("ollama-url", "http://localhost:11434", "Ollama server URL")
	serveCmd.Flags().String("ollama-model", "nomic-embed-text", "Ollama embedding model")
	serveCmd.Flags().Bool("enable-pipeline", false, "Enable the menutracking regulatory tracking pipeline (requires postgres-dsn)")

	_ = viper.BindPFlags(serveCmd.Flags())
	_ = viper.BindEnv("admin-email", "ADMIN_EMAIL")
	_ = viper.BindEnv("postgres-dsn", "POSTGRES_DSN")
}