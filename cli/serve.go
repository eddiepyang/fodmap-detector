package cli

import (
	"context"
	"fmt"
	"os"

	"fodmap/server"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP analysis server.",
	RunE: func(cmd *cobra.Command, args []string) error {
		port := viper.GetInt("port")
		weaviateHost := viper.GetString("weaviate")
		chatAPIKey := viper.GetString("chat-api-key")
		chatModel := viper.GetString("chat-model")
		filterModel := viper.GetString("filter-model")

		if port <= 0 || port > 65535 {
			return fmt.Errorf("invalid port: %d (must be between 1 and 65535)", port)
		}
		if chatAPIKey != "" && chatModel == "" {
			return fmt.Errorf("chat-model cannot be empty when chat-api-key is provided")
		}

		srv, err := server.New(context.Background(), server.Config{
			Port:         port,
			WeaviateHost: weaviateHost,
			GeminiAPIKey: os.Getenv("GEMINI_API_KEY"),
			ChatModel:    chatModel,
			FilterModel:  filterModel,
			ChatAPIKey:   chatAPIKey,
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
}
