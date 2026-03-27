package cli

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fodmap/data"
	"fodmap/data/schemas"
	"fodmap/search"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
	indexCmd.Flags().String("filter-city", "", "Filter reviews by city")
}

var vectorizerHTTPClient = &http.Client{Timeout: 5 * time.Minute}

type batchRequest struct {
	Texts []string `json:"texts"`
}

// decodeFloat32Vectors reads a binary stream of the form
// [header (headerLen bytes of uint32 LE fields)][float32 LE data].
// The first uint32 is the number of rows, the second is the dimension.
// Returns the decoded vectors as a slice of []float32.
func decodeFloat32Vectors(r io.Reader, header []byte) ([][]float32, error) {
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	rows := binary.LittleEndian.Uint32(header[:4])
	dim := binary.LittleEndian.Uint32(header[4:8])

	raw := make([]byte, rows*dim*4)
	if _, err := io.ReadFull(r, raw); err != nil {
		return nil, fmt.Errorf("read vectors: %w", err)
	}

	vectors := make([][]float32, rows)
	for i := range vectors {
		vec := make([]float32, dim)
		off := i * int(dim) * 4
		for j := range vec {
			vec[j] = math.Float32frombits(binary.LittleEndian.Uint32(raw[off+j*4:]))
		}
		vectors[i] = vec
	}
	return vectors, nil
}

// vectorizeBatch sends all texts in a single HTTP request to the vectorizer's
// batch endpoint, which runs one model.encode() GPU pass for the entire batch.
func vectorizeBatch(ctx context.Context, host string, items []search.IndexItem) error {
	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = item.Review.Text
	}

	body, err := json.Marshal(batchRequest{Texts: texts})
	if err != nil {
		return fmt.Errorf("marshal batch request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+host+"/vectors/batch", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := vectorizerHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("vectorize batch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode > 399 {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("vectorizer status %d: %s", resp.StatusCode, errBody)
	}

	var header [8]byte
	vectors, err := decodeFloat32Vectors(resp.Body, header[:])
	if err != nil {
		return fmt.Errorf("decode vectors: %w", err)
	}
	if len(vectors) != len(items) {
		return fmt.Errorf("vectorizer returned %d vectors, expected %d", len(vectors), len(items))
	}

	for i, vec := range vectors {
		items[i].Vector = vec
	}
	return nil
}

func runIndex(cmd *cobra.Command, _ []string) error {
	host := viper.GetString("weaviate")
	batchSize := viper.GetInt("batch-size")
	numWorkers := viper.GetInt("workers")
	archivePath := viper.GetString("archive")
	checkpointPath := viper.GetString("checkpoint")
	startOffset := viper.GetInt("start-offset")
	vectorizerHost := viper.GetString("vectorizer")
	filterCity := viper.GetString("filter-city")
	ctx := context.Background()

	if batchSize <= 0 {
		return fmt.Errorf("batch-size must be greater than 0")
	}
	if numWorkers <= 0 {
		return fmt.Errorf("workers must be greater than 0")
	}

	if vectorizerHost != "" {
		if _, _, err := net.SplitHostPort(vectorizerHost); err != nil {
			return fmt.Errorf("invalid --vectorizer value %q: must be host:port", vectorizerHost)
		}
	}

	client, err := search.NewClient(host)
	if err != nil {
		return fmt.Errorf("weaviate client: %w", err)
	}

	if err := client.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("schema init: %w", err)
	}

	slog.Info("loading business metadata")
	businessMap, err := data.GetBusinessMap("")
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

	// Deep buffers so the producer stays ahead of the GPU and the GPU stays
	// ahead of the Weaviate upload workers — minimising idle time at each stage.
	// Deep buffers so the producer stays ahead of the GPU and Weaviate.
	rawLineCh := make(chan []byte, numWorkers*batchSize*2)
	batchCh := make(chan []search.IndexItem, numWorkers*4)
	vectorizedCh := make(chan []search.IndexItem, numWorkers*4)

	var (
		parseWg   sync.WaitGroup
		vecWg     sync.WaitGroup
		uploadWg  sync.WaitGroup
		total     atomic.Int64
		firstErr  atomic.Pointer[error]
	)

	total.Store(int64(offset))

	// Stage 1: Parallel JSON parsing workers
	for range numWorkers {
		parseWg.Add(1)
		go func() {
			defer parseWg.Done()
			batch := make([]search.IndexItem, 0, batchSize)
			for b := range rawLineCh {
				if firstErr.Load() != nil {
					continue
				}
				var r schemas.Review
				if err := json.Unmarshal(b, &r); err != nil {
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
				if filterCity != "" && !strings.EqualFold(filterCity, item.City) {
					continue
				}
				batch = append(batch, item)
				if len(batch) >= batchSize {
					batchCh <- append([]search.IndexItem(nil), batch...)
					batch = batch[:0]
				}
			}
			if len(batch) > 0 {
				batchCh <- append([]search.IndexItem(nil), batch...)
			}
		}()
	}
	go func() {
		parseWg.Wait()
		close(batchCh)
	}()

	// Stage 2: Parallel vectorizer workers
	vWorkers := numWorkers
	if vectorizerHost == "" {
		vWorkers = 1 // If skip vectorization, single pass-through worker is fine
	}
	for range vWorkers {
		vecWg.Add(1)
		go func() {
			defer vecWg.Done()
			for batch := range batchCh {
				if firstErr.Load() != nil {
					continue
				}
				if vectorizerHost != "" {
					var err error
					for attempt := 0; attempt < 5; attempt++ {
						err = vectorizeBatch(ctx, vectorizerHost, batch)
						if err == nil {
							break
						}
						slog.Warn("vectorize batch failed, retrying", "attempt", attempt+1, "error", err)
						time.Sleep(time.Duration(1<<attempt) * time.Second)
					}
					if err != nil {
						e := fmt.Errorf("vectorize batch failed after 5 attempts: %w", err)
						firstErr.CompareAndSwap(nil, &e)
						continue
					}
				}
				vectorizedCh <- batch
			}
		}()
	}
	go func() {
		vecWg.Wait()
		close(vectorizedCh)
	}()

	// Stage 3: Weaviate upload workers
	for range numWorkers {
		uploadWg.Add(1)
		go func() {
			defer uploadWg.Done()
			for batch := range vectorizedCh {
				if firstErr.Load() != nil {
					continue
				}
				var err error
				for attempt := 0; attempt < 5; attempt++ {
					err = client.BatchUpsert(ctx, batch)
					if err == nil {
						break
					}
					slog.Warn("batch upsert failed, retrying", "attempt", attempt+1, "error", err)
					time.Sleep(time.Duration(1<<attempt) * time.Second)
				}
				if err != nil {
					e := fmt.Errorf("batch upsert failed after 5 attempts: %w", err)
					firstErr.CompareAndSwap(nil, &e)
					continue
				}
				n := total.Add(int64(len(batch)))
				slog.Info("indexed batch", "total", n)

				if checkpointPath != "" {
					// Rewind the checkpoint behind n to cover batches still in-flight across
					// the three pipeline stages (rawLineCh, batchCh, vectorizedCh) plus active
					// workers. Upserts are idempotent (deterministic SHA1 UUIDs), so re-processing
					// these items on restart is safe — this only trades a small amount of
					// duplicate work for a guaranteed-consistent restart position.
					safeOffset := n - int64(8*numWorkers*batchSize)
					if safeOffset < 0 {
						safeOffset = 0
					}
					if werr := writeCheckpoint(checkpointPath, safeOffset); werr != nil {
						slog.Warn("checkpoint write failed", "error", werr)
					}
				}
			}
		}()
	}

	// Stage 0: Main thread reads raw lines from archive
	for scanner.Scan() {
		if firstErr.Load() != nil {
			break
		}
		b := make([]byte, len(scanner.Bytes()))
		copy(b, scanner.Bytes())
		rawLineCh <- b
	}
	close(rawLineCh)
	uploadWg.Wait()

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
