package cli

import (
	"fodmap/data"
	"log"

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
		fileScanner := data.GetArchive("review")
		log.Printf("created fileScanner")
		data.WriteBatchParquet(outputFile, fileScanner)
		log.Printf("created file")
		if _, err := data.ReadParquet(outputFile, 5); err != nil {
			log.Printf("Error reading parquet: %v", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(batchCmd)
	batchCmd.Flags().StringP("output", "o", "test.parquet", "Output file path")
}
