package cli

import (
	"fodmap/data"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

var batchCmd = &cobra.Command{
	Use:   "batch",
	Short: "Write data in Parquet format.",
	Run: func(cmd *cobra.Command, args []string) {
		outputFile, _ := cmd.Flags().GetString("output")
		if outputFile == "" {
			outputFile = "test.parquet"
		}
		fileScanner, err := data.GetArchive("review")
		if err != nil {
			slog.Error("opening archive", "error", err)
			os.Exit(1)
		}
		slog.Info("created fileScanner")
		if err := data.WriteBatchParquet(outputFile, fileScanner); err != nil {
			slog.Error("writing parquet", "error", err)
			os.Exit(1)
		}
		slog.Info("created file")
		if _, err := data.ReadParquet(outputFile, 5); err != nil {
			slog.Error("error reading parquet", "error", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(batchCmd)
	batchCmd.Flags().StringP("output", "o", "test.parquet", "Output file path")
}
