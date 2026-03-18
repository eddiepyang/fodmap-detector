package cli

import (
	"encoding/json"
	"fmt"
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
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFile, _ := cmd.Flags().GetString("output")
		if outputFile == "" {
			outputFile = "test.avro"
		}
		fileScanner, closer, err := data.GetArchive(data.DefaultArchivePath, "review")
		if err != nil {
			return fmt.Errorf("opening archive: %w", err)
		}
		defer closer.Close()
		slog.Info("created fileScanner")

		f, err := os.Create(outputFile)
		if err != nil {
			return fmt.Errorf("creating output file: %w", err)
		}
		w, err := dataio.NewEventWriter(f, schemas.EventSchema)
		if err != nil {
			return fmt.Errorf("creating event writer: %w", err)
		}
		defer w.Close()

		for fileScanner.Scan() {
			var record map[string]any
			if err := json.Unmarshal(fileScanner.Bytes(), &record); err != nil {
				return fmt.Errorf("unmarshal record: %w", err)
			}
			if err := w.Write(record); err != nil {
				return fmt.Errorf("write record: %w", err)
			}
		}
		if err := fileScanner.Err(); err != nil {
			return fmt.Errorf("scanner error: %w", err)
		}
		slog.Info("created file")
		return nil
	},
}

var eventReadCmd = &cobra.Command{
	Use:   "read",
	Short: "Read event data from an Avro file.",
	RunE: func(cmd *cobra.Command, args []string) error {
		inputFile, _ := cmd.Flags().GetString("input")
		if inputFile == "" {
			return fmt.Errorf("input file path is required")
		}
		if err := dataio.ReadFile(inputFile); err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(eventCmd)
	eventCmd.AddCommand(eventWriteCmd)
	eventCmd.AddCommand(eventReadCmd)

	eventWriteCmd.Flags().StringP("output", "o", "test.avro", "Output file path")
	eventReadCmd.Flags().StringP("input", "i", "", "Input file path")
}
