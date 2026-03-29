package cli

import (
	"context"
	"fmt"
	"os"

	"fodmap/auth"
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

		dbPath := viper.GetString("db")
		storeType := viper.GetString("store-type")
		postgresDSN := viper.GetString("postgres-dsn")
		jwtSecret := viper.GetString("jwt-secret")
		pineconeAPIKey := viper.GetString("pinecone-api-key")
		pineconeIndexHost := viper.GetString("pinecone-index-host")
		vectorizerURL := viper.GetString("vectorizer-url")
		if jwtSecret == "" {
			jwtSecret = os.Getenv("JWT_SECRET")
		}
		if jwtSecret == "" {
			jwtSecret = "change-me-in-production"
		}
		if jwtSecret == "" {
			jwtSecret = "change-me-in-production" // Fallback but warn or error?
		}

		var userStore auth.Store
		var err error

		if storeType == "postgres" {
			if postgresDSN == "" {
				return fmt.Errorf("postgres-dsn is required when store-type is postgres")
			}
			userStore, err = auth.NewPostgresStore(context.Background(), postgresDSN)
		} else {
			userStore, err = auth.NewSQLiteStore(dbPath)
		}

		if err != nil {
			return fmt.Errorf("initializing user store: %w", err)
		}
		defer userStore.Close()

		if port <= 0 || port > 65535 {
			return fmt.Errorf("invalid port: %d (must be between 1 and 65535)", port)
		}
		if chatAPIKey != "" && chatModel == "" {
			return fmt.Errorf("chat-model cannot be empty when chat-api-key is provided")
		}

		srv, err := server.New(context.Background(), server.Config{
			Port:               port,
			WeaviateHost:       weaviateHost,
			GeminiAPIKey:       os.Getenv("GOOGLE_API_KEY"),
			ChatModel:          chatModel,
			FilterModel:        filterModel,
			ChatAPIKey:         chatAPIKey,
			CORSAllowedOrigins: corsOrigins,
			UserStore:          userStore,
			JWTSecret:          jwtSecret,
			PineconeAPIKey:     pineconeAPIKey,
			PineconeIndexHost:  pineconeIndexHost,
			VectorizerURL:      vectorizerURL,
		})
		if err != nil {
			return fmt.Errorf("initializing server: %w", err)
		}
		if err := srv.Start(); err != nil {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().IntP("port", "p", 8081, "Port to listen on")
	serveCmd.Flags().String("weaviate", "", "Weaviate host:port (e.g. localhost:8090); omit to disable search")
	serveCmd.Flags().String("chat-api-key", "", "Bearer token for /chat endpoint; omit to disable chat")
	serveCmd.Flags().String("chat-model", "gemini-3-flash-preview", "Gemini model ID for chat sessions")
	serveCmd.Flags().String("filter-model", "gemini-3.1-flash-lite-preview", "Gemini model ID for topic filtering")
	serveCmd.Flags().StringSlice("cors-origins", []string{"http://localhost:3000", "https://app.example.com"}, "Comma-separated list of allowed CORS origins")
	serveCmd.Flags().String("db", "fodmap.db", "Path to the SQLite database for user storage")
	serveCmd.Flags().String("store-type", "sqlite", "Store backend to use: sqlite or postgres")
	serveCmd.Flags().String("postgres-dsn", "", "PostgreSQL connection string (required if store-type is postgres)")
	serveCmd.Flags().String("jwt-secret", "", "Secret key for JWT signing (or use JWT_SECRET env var)")
	serveCmd.Flags().String("pinecone-api-key", "", "Pinecone API Key")
	serveCmd.Flags().String("pinecone-index-host", "", "Pinecone Index Host (e.g. https://index-name.svc.pinecone.io)")
	serveCmd.Flags().String("vectorizer-url", "http://localhost:8000", "Base URL for the vectorizer-proxy")

	_ = viper.BindPFlags(serveCmd.Flags())
}
