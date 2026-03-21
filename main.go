package main

import (
	"log/slog"
	"os"

	"fodmap/cli"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	cli.Execute()
}