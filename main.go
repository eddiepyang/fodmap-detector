package main

import (
	"log/slog"
	"os"

	"fodmap/cli"
)

func main() {
	var level slog.Level
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	} else {
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, opts)))

	cli.Execute()
}
