package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fodmap/data"
	"fodmap/data/schemas"
	"fodmap/search"
	"fodmap/server"

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
	indexCmd.Flags().String("weaviate-scheme", "http", "Weaviate scheme (http or https)")
	indexCmd.Flags().String("weaviate-api-key", "", "Weaviate API Key (for Weaviate Cloud)")
	indexCmd.Flags().Int("batch-size", 512, "Number of reviews per Weaviate batch")
	indexCmd.Flags().Int("workers", 4, "Number of concurrent batch upload goroutines")
	indexCmd.Flags().String("archive", data.DefaultArchivePath, "Path to the Yelp dataset TAR archive")
	indexCmd.Flags().String("checkpoint", "index.checkpoint", "Path to checkpoint file (empty string disables checkpointing)")
	indexCmd.Flags().Int("start-offset", 0, "Skip this many reviews before indexing (overrides checkpoint)")
	indexCmd.Flags().String("vectorizer", "", "t2v-transformers host:port for direct pre-vectorization (e.g. localhost:8091); empty = use model-path")
	indexCmd.Flags().String("model-path", "", "Path to GGUF embedding model for in-process vectorization")
	indexCmd.Flags().String("filter-city", "", "Filter reviews by city")
	indexCmd.Flags().Bool("postgres-search", false, "Use PostgreSQL (pgvector) for vector search instead of Weaviate/Pinecone")
	indexCmd.Flags().String("postgres-dsn", "", "PostgreSQL connection string (required if postgres-search is true)")
	indexCmd.Flags().String("pinecone-api-key", "", "Pinecone API Key")
	indexCmd.Flags().String("pinecone-index-host", "", "Pinecone Index Host (e.g. https://index-name.svc.pinecone.io)")
}


func runIndex(cmd *cobra.Command, _ []string) error {
	host := viper.GetString("weaviate")
	scheme := viper.GetString("weaviate-scheme")
	if scheme == "" {
		scheme = os.Getenv("WEAVIATE_SCHEME")
	}
	apiKey := viper.GetString("weaviate-api-key")
	if apiKey == "" {
		apiKey = os.Getenv("WEAVIATE_API_KEY")
	}
	batchSize := viper.GetInt("batch-size")
	numWorkers := viper.GetInt("workers")
	archivePath := viper.GetString("archive")
	checkpointPath := viper.GetString("checkpoint")
	startOffset := viper.GetInt("start-offset")
	vectorizerHost := viper.GetString("vectorizer")
	modelPath := viper.GetString("model-path")
	filterCity := viper.GetString("filter-city")
	ctx := context.Background()

	if batchSize <= 0 {
		return fmt.Errorf("batch-size must be greater than 0")
	}
	if numWorkers <= 0 {
		return fmt.Errorf("workers must be greater than 0")
	}

	postgresSearch := viper.GetBool("postgres-search")
	postgresDSN := viper.GetString("postgres-dsn")
	pineconeAPIKey := viper.GetString("pinecone-api-key")
	pineconeIndexHost := viper.GetString("pinecone-index-host")

	// Create embedder: prefer in-process llama-go, fall back to HTTP vectorizer.
	var embedder search.Embedder
	if modelPath != "" {
		var err error
		embedder, err = search.NewLlamaEmbedder(modelPath)
		if err != nil {
			return fmt.Errorf("loading embedding model: %w", err)
		}
		defer embedder.Close()
		slog.Info("in-process embedder loaded", "model", modelPath)
	} else if vectorizerHost != "" {
		if _, _, err := net.SplitHostPort(vectorizerHost); err != nil {
			return fmt.Errorf("invalid --vectorizer value %q: must be host:port", vectorizerHost)
		}
		embedder = search.NewVectorizerClient("http://" + vectorizerHost)
		slog.Info("using HTTP vectorizer", "host", vectorizerHost)
	}

	var client server.Searcher
	if postgresSearch && postgresDSN != "" {
		sc, err := search.NewPostgresClient(postgresDSN, embedder)
		if err != nil {
			return fmt.Errorf("postgres client: %w", err)
		}
		client = sc
	} else if pineconeAPIKey != "" && pineconeIndexHost != "" {
		client = search.NewPineconeClient(pineconeAPIKey, pineconeIndexHost, embedder)
	} else {
		sc, err := search.NewClient(host, scheme, apiKey, embedder)
		if err != nil {
			return fmt.Errorf("weaviate client: %w", err)
		}
		client = sc
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
		parseWg  sync.WaitGroup
		vecWg    sync.WaitGroup
		uploadWg sync.WaitGroup
		total    atomic.Int64
		firstErr atomic.Pointer[error]
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
				if vectorizerHost != "" || modelPath != "" {
					var err error
					for attempt := 0; attempt < 5; attempt++ {
						texts := make([]string, len(batch))
						for i, item := range batch {
							texts[i] = "search_document: " + item.Review.Text
						}
						var vecs [][]float32
						vecs, err = embedder.EmbedBatch(ctx, texts)
						if err == nil {
							for i, vec := range vecs {
								batch[i].Vector = vec
							}
							break
						}
						slog.Warn("embed batch failed, retrying", "attempt", attempt+1, "error", err)
						time.Sleep(time.Duration(1<<attempt) * time.Second)
					}
					if err != nil {
						e := fmt.Errorf("embed batch failed after 5 attempts: %w", err)
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
