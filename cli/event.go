package cli

import (
	"fodmap/data"
	"fodmap/data/io"
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
		fileScanner := data.GetArchive("review")
		slog.Info("created fileScanner")
		io.WriteEventFile(fileScanner, outputFile, schemas.EventSchema)
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
		err := io.ReadFile(inputFile)
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
