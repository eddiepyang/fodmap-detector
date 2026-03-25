package cli

import (
	"context"
	"fmt"
	"os"

	"fodmap/server"

	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP analysis server.",
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		weaviateHost, _ := cmd.Flags().GetString("weaviate")
		chatAPIKey, _ := cmd.Flags().GetString("chat-api-key")
		geminiModel, _ := cmd.Flags().GetString("gemini-model")

		srv, err := server.New(context.Background(), server.Config{
			Port:         port,
			WeaviateHost: weaviateHost,
			GeminiAPIKey: os.Getenv("GEMINI_API_KEY"),
			GeminiModel:  geminiModel,
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
	serveCmd.Flags().IntP("port", "p", 8080, "Port to listen on")
	serveCmd.Flags().String("weaviate", "", "Weaviate host:port (e.g. localhost:8090); omit to disable search")
	serveCmd.Flags().String("chat-api-key", "", "Bearer token for /chat endpoint; omit to disable chat")
	serveCmd.Flags().String("gemini-model", "gemini-3.1-flash", "Gemini model ID for chat sessions")
}
