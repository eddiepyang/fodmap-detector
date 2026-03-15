package cli

import (
	"encoding/json"
	"fodmap/data"
	dataio "fodmap/data/io"
	"fodmap/data/schemas"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

var eventCmd = &cobra.Command{
	Use:   "event",
	Short: "Work with event data.",
}

var eventWriteCmd = &cobra.Command{
	Use:   "write",
	Short: "Write event data to an Avro file.",
	Run: func(cmd *cobra.Command, args []string) {
		outputFile, _ := cmd.Flags().GetString("output")
		if outputFile == "" {
			outputFile = "test.avro"
		}
		fileScanner, err := data.GetArchive("review")
		if err != nil {
			slog.Error("opening archive", "error", err)
			os.Exit(1)
		}
		slog.Info("created fileScanner")

		f, err := os.Create(outputFile)
		if err != nil {
			slog.Error("creating output file", "error", err)
			os.Exit(1)
		}
		w, err := dataio.NewEventWriter(f, schemas.EventSchema)
		if err != nil {
			slog.Error("creating event writer", "error", err)
			os.Exit(1)
		}
		defer w.Close()

		for fileScanner.Scan() {
			var record map[string]any
			if err := json.Unmarshal(fileScanner.Bytes(), &record); err != nil {
				slog.Error("unmarshal record", "error", err)
				os.Exit(1)
			}
			if err := w.Write(record); err != nil {
				slog.Error("write record", "error", err)
				os.Exit(1)
			}
		}
		if err := fileScanner.Err(); err != nil {
			slog.Error("scanner error", "error", err)
			os.Exit(1)
		}
		slog.Info("created file")
	},
}

var eventReadCmd = &cobra.Command{
	Use:   "read",
	Short: "Read event data from an Avro file.",
	Run: func(cmd *cobra.Command, args []string) {
		inputFile, _ := cmd.Flags().GetString("input")
		if inputFile == "" {
			slog.Error("input file path is required")
			os.Exit(1)
		}
		err := dataio.ReadFile(inputFile)
		if err != nil {
			slog.Error("error reading file", "error", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(eventCmd)
	eventCmd.AddCommand(eventWriteCmd)
	eventCmd.AddCommand(eventReadCmd)

	eventWriteCmd.Flags().StringP("output", "o", "test.avro", "Output file path")
	eventReadCmd.Flags().StringP("input", "i", "", "Input file path")
}
