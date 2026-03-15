package cli

import (
	"context"
	"fmt"

	"fodmap/server"

	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP analysis server.",
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		promptPath, _ := cmd.Flags().GetString("prompt")
		weaviateHost, _ := cmd.Flags().GetString("weaviate")

		srv, err := server.New(context.Background(), server.Config{
			Port:         port,
			PromptPath:   promptPath,
			WeaviateHost: weaviateHost,
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
	serveCmd.Flags().String("prompt", "./prompt.txt", "Path to the LLM prompt template")
	serveCmd.Flags().String("weaviate", "", "Weaviate host:port (e.g. localhost:8090); omit to disable search")
}
