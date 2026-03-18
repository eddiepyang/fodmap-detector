package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"sync/atomic"

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
	indexCmd.Flags().Int("batch-size", 500, "Number of reviews per Weaviate batch")
	indexCmd.Flags().Int("workers", 4, "Number of concurrent batch upload goroutines")
	indexCmd.Flags().String("archive", data.DefaultArchivePath, "Path to the Yelp dataset TAR archive")
	indexCmd.Flags().String("checkpoint", "index.checkpoint", "Path to checkpoint file (empty string disables checkpointing)")
	indexCmd.Flags().Int("start-offset", 0, "Skip this many reviews before indexing (overrides checkpoint)")
}

func runIndex(cmd *cobra.Command, _ []string) error {
	host, _ := cmd.Flags().GetString("weaviate")
	batchSize, _ := cmd.Flags().GetInt("batch-size")
	numWorkers, _ := cmd.Flags().GetInt("workers")
	archivePath, _ := cmd.Flags().GetString("archive")
	checkpointPath, _ := cmd.Flags().GetString("checkpoint")
	startOffset, _ := cmd.Flags().GetInt("start-offset")
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

	offset := 0
	if startOffset > 0 {
		offset = startOffset
		slog.Info("starting from explicit offset", "offset", offset)
	} else if checkpointPath != "" {
		offset, err = readCheckpoint(checkpointPath)
		if err != nil {
			return fmt.Errorf("reading checkpoint: %w", err)
		}
		if offset > 0 {
			slog.Info("resuming from checkpoint", "offset", offset)
		}
	}

	scanner, closer, err := data.GetArchive(archivePath, "review")
	if err != nil {
		return fmt.Errorf("opening archive: %w", err)
	}
	defer closer.Close()
	buf := make([]byte, 4*1024*1024)
	scanner.Buffer(buf, 4*1024*1024)

	// Fast-forward past already-indexed records.
	for i := 0; i < offset; i++ {
		if !scanner.Scan() {
			break
		}
	}

	slog.Info("archive opened, beginning indexing")

	batchCh := make(chan []search.IndexItem, numWorkers)

	var (
		wg       sync.WaitGroup
		total    atomic.Int64
		firstErr atomic.Pointer[error]
	)

	total.Store(int64(offset))

	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range batchCh {
				if firstErr.Load() != nil {
					continue // drain without processing to unblock producer
				}
				if err := client.BatchUpsert(ctx, batch); err != nil {
					e := fmt.Errorf("batch upsert: %w", err)
					firstErr.CompareAndSwap(nil, &e)
					continue
				}
				n := total.Add(int64(len(batch)))
				slog.Info("indexed batch", "total", n)

				if checkpointPath != "" {
					if werr := writeCheckpoint(checkpointPath, n); werr != nil {
						slog.Warn("checkpoint write failed", "error", werr)
					}
				}
			}
		}()
	}

	batch := make([]search.IndexItem, 0, batchSize)

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
			batchCh <- append([]search.IndexItem(nil), batch...)
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		batchCh <- batch
	}
	close(batchCh)
	wg.Wait()

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %w", err)
	}

	if p := firstErr.Load(); p != nil {
		return *p
	}

	slog.Info("indexing complete", "total_reviews", total.Load())
	return nil
}

// readCheckpoint returns the number of reviews previously indexed from path.
// Returns 0 if the file does not exist.
func readCheckpoint(path string) (int, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read checkpoint: %w", err)
	}
	n, err := strconv.Atoi(string(b))
	if err != nil {
		return 0, fmt.Errorf("parse checkpoint: %w", err)
	}
	return n, nil
}

// writeCheckpoint atomically persists n to path via a temp file and rename.
func writeCheckpoint(path string, n int64) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(n, 10)), 0o644); err != nil {
		return fmt.Errorf("write checkpoint tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename checkpoint: %w", err)
	}
	return nil
}
