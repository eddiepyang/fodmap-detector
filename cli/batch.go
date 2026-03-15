package cli

import (
	"fmt"
	"fodmap/data"
	"log/slog"

	"github.com/spf13/cobra"
)

var batchCmd = &cobra.Command{
	Use:   "batch",
	Short: "Write data in Parquet format.",
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFile, _ := cmd.Flags().GetString("output")
		if outputFile == "" {
			outputFile = "test.parquet"
		}
		fileScanner, closer, err := data.GetArchive("review")
		if err != nil {
			return fmt.Errorf("opening archive: %w", err)
		}
		defer closer.Close()
		slog.Info("created fileScanner")
		if err := data.WriteBatchParquet(outputFile, fileScanner); err != nil {
			return fmt.Errorf("writing parquet: %w", err)
		}
		slog.Info("created file")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(batchCmd)
	batchCmd.Flags().StringP("output", "o", "test.parquet", "Output file path")
}
