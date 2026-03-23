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

	// Deep buffers so the producer stays ahead of the GPU and the GPU stays
	// ahead of the Weaviate upload workers — minimising idle time at each stage.
	batchCh := make(chan []search.IndexItem, numWorkers*4)
	vectorizedCh := make(chan []search.IndexItem, numWorkers*2)

	var (
		wg       sync.WaitGroup
		total    atomic.Int64
		firstErr atomic.Pointer[error]
	)

	total.Store(int64(offset))

	// Stage 1: single vectorize worker — the proxy serialises GPU work anyway,
	// so one worker is enough; the deep batchCh buffer ensures the next request
	// is ready the moment the current one finishes.
	go func() {
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
			vectorizedCh <- batch
		}
		close(vectorizedCh)
	}()

	// Stage 2: upload workers — drain vectorizedCh while Stage 1 keeps running.
	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range vectorizedCh {
				if firstErr.Load() != nil {
					continue
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
