package main

import (
	"context"
	"log/slog"
	"os"

	"fodmap/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	srv, err := server.New(context.Background(), server.Config{
		Port:       8080,
		PromptPath: "./prompt.txt",
	})
	if err != nil {
		slog.Error("failed to initialize server", "error", err)
		os.Exit(1)
	}
	if err := srv.Start(); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}