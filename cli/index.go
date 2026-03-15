package cli

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"

	"fodmap/data"
	"fodmap/data/schemas"
	"fodmap/search"

	"github.com/spf13/cobra"
)

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Index all reviews into Weaviate for semantic search.",
	Run:   runIndex,
}

func init() {
	rootCmd.AddCommand(indexCmd)
	indexCmd.Flags().String("weaviate", "localhost:8090", "Weaviate host:port")
	indexCmd.Flags().Int("batch-size", 100, "Number of reviews per Weaviate batch")
}

func runIndex(cmd *cobra.Command, _ []string) {
	host, _ := cmd.Flags().GetString("weaviate")
	batchSize, _ := cmd.Flags().GetInt("batch-size")
	ctx := context.Background()

	client, err := search.NewClient(host)
	if err != nil {
		slog.Error("weaviate client", "error", err)
		os.Exit(1)
	}

	if err := client.EnsureSchema(ctx); err != nil {
		slog.Error("schema init", "error", err)
		os.Exit(1)
	}

	slog.Info("loading business metadata")
	businessMap, err := data.GetBusinessMap()
	if err != nil {
		slog.Error("loading business map", "error", err)
		os.Exit(1)
	}
	slog.Info("business metadata loaded", "count", len(businessMap))

	scanner := data.GetArchive("review")
	// GetArchive returns a plain bufio.Scanner with the default 64 KB buffer.
	// Yelp reviews can be several KB; use a 4 MB buffer to match GetReviewsByBusiness.
	buf := make([]byte, 4*1024*1024)
	scanner.Buffer(buf, 4*1024*1024)

	slog.Info("archive opened, beginning indexing")

	batch := make([]search.IndexItem, 0, batchSize)
	total := 0

	flush := func() {
		if err := client.BatchUpsert(ctx, batch); err != nil {
			slog.Error("batch upsert failed", "error", err, "total_so_far", total)
			os.Exit(1)
		}
		total += len(batch)
		slog.Info("indexed batch", "total", total)
		batch = batch[:0]
	}

	for scanner.Scan() {
		var r schemas.ReviewSchemaS
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			slog.Warn("skipping malformed record", "error", err)
			continue
		}

		item := search.IndexItem{Review: r}
		if biz, ok := businessMap[r.BusinessId]; ok {
			item.City = biz.City
			item.State = biz.State
			item.Categories = biz.Categories
		}

		batch = append(batch, item)
		if len(batch) >= batchSize {
			flush()
		}
	}

	if len(batch) > 0 {
		flush()
	}

	if err := scanner.Err(); err != nil {
		slog.Error("scanner error", "error", err)
		os.Exit(1)
	}

	slog.Info("indexing complete", "total_reviews", total)
}
