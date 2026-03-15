package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"fodmap/data"
	"fodmap/data/schemas"
	"fodmap/search"

	"github.com/spf13/cobra"
)

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Index all reviews into Weaviate for semantic search.",
	RunE:  runIndex,
}

func init() {
	rootCmd.AddCommand(indexCmd)
	indexCmd.Flags().String("weaviate", "localhost:8090", "Weaviate host:port")
	indexCmd.Flags().Int("batch-size", 100, "Number of reviews per Weaviate batch")
}

func runIndex(cmd *cobra.Command, _ []string) error {
	host, _ := cmd.Flags().GetString("weaviate")
	batchSize, _ := cmd.Flags().GetInt("batch-size")
	ctx := context.Background()

	client, err := search.NewClient(host)
	if err != nil {
		return fmt.Errorf("weaviate client: %w", err)
	}

	if err := client.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("schema init: %w", err)
	}

	slog.Info("loading business metadata")
	businessMap, err := data.GetBusinessMap()
	if err != nil {
		return fmt.Errorf("loading business map: %w", err)
	}
	slog.Info("business metadata loaded", "count", len(businessMap))

	scanner, closer, err := data.GetArchive("review")
	if err != nil {
		return fmt.Errorf("opening archive: %w", err)
	}
	defer closer.Close()
	buf := make([]byte, 4*1024*1024)
	scanner.Buffer(buf, 4*1024*1024)

	slog.Info("archive opened, beginning indexing")

	batch := make([]search.IndexItem, 0, batchSize)
	total := 0

	flush := func() error {
		if err := client.BatchUpsert(ctx, batch); err != nil {
			return fmt.Errorf("batch upsert failed (total so far: %d): %w", total, err)
		}
		total += len(batch)
		slog.Info("indexed batch", "total", total)
		batch = batch[:0]
		return nil
	}

	for scanner.Scan() {
		var r schemas.Review
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			slog.Warn("skipping malformed record", "error", err)
			continue
		}

		item := search.IndexItem{Review: r}
		if biz, ok := businessMap[r.BusinessID]; ok {
			item.City = biz.City
			item.State = biz.State
			item.Categories = biz.Categories
		}

		batch = append(batch, item)
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}

	if len(batch) > 0 {
		if err := flush(); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %w", err)
	}

	slog.Info("indexing complete", "total_reviews", total)
	return nil
}
