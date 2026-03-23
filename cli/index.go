package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

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
	indexCmd.Flags().Int("batch-size", 512, "Number of reviews per Weaviate batch")
	indexCmd.Flags().Int("workers", 4, "Number of concurrent batch upload goroutines")
	indexCmd.Flags().String("archive", data.DefaultArchivePath, "Path to the Yelp dataset TAR archive")
	indexCmd.Flags().String("checkpoint", "index.checkpoint", "Path to checkpoint file (empty string disables checkpointing)")
	indexCmd.Flags().Int("start-offset", 0, "Skip this many reviews before indexing (overrides checkpoint)")
	indexCmd.Flags().String("vectorizer", "", "t2v-transformers host:port for direct pre-vectorization (e.g. localhost:8091); empty = Weaviate vectorizes")
}

var vectorizerHTTPClient = &http.Client{Timeout: 30 * time.Second}

type transformerRequest struct {
	Text   string                `json:"text"`
	Config transformerReqConfig  `json:"config"`
}

type transformerReqConfig struct {
	PoolingStrategy string `json:"pooling_strategy"`
}

type transformerResponse struct {
	Vector []float32 `json:"vector"`
	Error  string    `json:"error"`
}

// vectorizeBatch calls the transformer sidecar directly for all items concurrently,
// setting each item's Vector field. Concurrent requests arrive within
// BATCH_WAIT_TIME_SECONDS and are batched by the transformer for a single GPU pass.
func vectorizeBatch(ctx context.Context, host string, items []search.IndexItem) error {
	type result struct {
		idx int
		vec []float32
		err error
	}
	ch := make(chan result, len(items))
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Go(func() {
			body, err := json.Marshal(transformerRequest{
				Text:   item.Review.Text,
				Config: transformerReqConfig{PoolingStrategy: "masked_mean"},
			})
			if err != nil {
				ch <- result{idx: i, err: err}
				return
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				"http://"+host+"/vectors", bytes.NewReader(body))
			if err != nil {
				ch <- result{idx: i, err: err}
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := vectorizerHTTPClient.Do(req)
			if err != nil {
				ch <- result{idx: i, err: err}
				return
			}
			defer resp.Body.Close()
			var res transformerResponse
			if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
				ch <- result{idx: i, err: fmt.Errorf("decode: %w", err)}
				return
			}
			if resp.StatusCode > 399 {
				ch <- result{idx: i, err: fmt.Errorf("vectorizer status %d: %s", resp.StatusCode, res.Error)}
				return
			}
			ch <- result{idx: i, vec: res.Vector}
		})
	}
	wg.Wait()
	for range items {
		r := <-ch
		if r.err != nil {
			return fmt.Errorf("vectorize item %d: %w", r.idx, r.err)
		}
		items[r.idx].Vector = r.vec
	}
	return nil
}

func runIndex(cmd *cobra.Command, _ []string) error {
	host, _ := cmd.Flags().GetString("weaviate")
	batchSize, _ := cmd.Flags().GetInt("batch-size")
	numWorkers, _ := cmd.Flags().GetInt("workers")
	archivePath, _ := cmd.Flags().GetString("archive")
	checkpointPath, _ := cmd.Flags().GetString("checkpoint")
	startOffset, _ := cmd.Flags().GetInt("start-offset")
	vectorizerHost, _ := cmd.Flags().GetString("vectorizer")
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
				if vectorizerHost != "" {
					if err := vectorizeBatch(ctx, vectorizerHost, batch); err != nil {
						e := fmt.Errorf("vectorize batch: %w", err)
						firstErr.CompareAndSwap(nil, &e)
						continue
					}
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
			item.BusinessName = biz.Name
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
