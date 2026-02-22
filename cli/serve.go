package cli

import (
	"context"
	"log/slog"
	"os"

	"fodmap/server"

	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP analysis server.",
	Run: func(cmd *cobra.Command, args []string) {
		port, _ := cmd.Flags().GetInt("port")
		promptPath, _ := cmd.Flags().GetString("prompt")

		srv, err := server.New(context.Background(), server.Config{
			Port:       port,
			PromptPath: promptPath,
		})
		if err != nil {
			slog.Error("failed to initialize server", "error", err)
			os.Exit(1)
		}
		if err := srv.Start(); err != nil {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().IntP("port", "p", 8080, "Port to listen on")
	serveCmd.Flags().String("prompt", "./prompt.txt", "Path to the LLM prompt template")
}
