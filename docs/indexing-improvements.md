# Plan: Speed Up Weaviate Indexing

## Context

The `index` command currently uploads batches sequentially — the scanner is idle while waiting for each `BatchUpsert` HTTP round-trip. With ~7M reviews at batch size 100, this creates ~70,000 sequential HTTP calls to Weaviate. Two changes eliminate this bottleneck:

1. **Concurrent batch workers** — pipeline scan and upload via a channel so N batches are in-flight simultaneously (biggest win)
2. **Larger default batch size** — reduce HTTP overhead per review (easy win, already flag-controlled)

## Changes

### File: `cli/index.go`

**Add `--workers` flag** (default: 4):
```go
indexCmd.Flags().Int("workers", 4, "Number of concurrent batch upload goroutines")
```

**Refactor `runIndex`** to producer/consumer pattern:

- Replace `total int` with `var total atomic.Int64`
- Add `workers, _ := cmd.Flags().GetInt("workers")`
- Create buffered work channel: `batchCh := make(chan []search.IndexItem, workers)`
- Start N worker goroutines (each ranges over `batchCh`, calls `BatchUpsert`, updates atomic total, logs progress)
- Use `var firstErr atomic.Pointer[error]` to capture first worker error (no error channel needed)
- Main loop sends **cloned** batches: `batchCh <- append([]search.IndexItem(nil), batch...)`
- After scan loop: `close(batchCh)`, then `wg.Wait()`, then check `firstErr`

**Change default batch size** from `100` -> `500`.

**Add `--archive` flag** (default: `data.DefaultArchivePath`):
```go
indexCmd.Flags().String("archive", data.DefaultArchivePath, "Path to the Yelp dataset TAR archive")
```
Pass the flag value to `GetArchive`.

### File: `data/data.go`

- Rename unexported `archiveGz` constant to exported `DefaultArchivePath = "../data/yelp_dataset.tar"`
- Change `GetArchive(fileName string)` to `GetArchive(archivePath, fileName string)` — pass `archivePath` to `os.Open` instead of the constant
- Update the two other callers (`cli/batch.go`, `cli/event.go`) to pass `data.DefaultArchivePath`

### Concurrency pattern (follows CLAUDE.md):
- Workers use `for range batchCh` (not select + done channel)
- On error, workers **continue draining** (`continue` inside the range loop) — never return early, so producer never deadlocks on a full channel
- Buffered channel: `make(chan []search.IndexItem, workers)` — natural backpressure
- `sync.WaitGroup` + `atomic.Pointer[error]` — no new dependencies needed

## Critical Files
- [cli/index.go](cli/index.go) — batch size, workers, checkpoint, archive flag
- [data/data.go](data/data.go) — `GetArchive` signature + exported default path constant
- [docker-compose.yaml](docker-compose.yaml) — optional CUDA enablement

### Optional: NVIDIA GPU acceleration (`docker-compose.yaml`)

If running on a Linux host with NVIDIA drivers + [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html):

```yaml
t2v-transformers:
  image: semitechnologies/transformers-inference:sentence-transformers-multi-qa-MiniLM-L6-cos-v1
  environment:
    ENABLE_CUDA: "1"
  deploy:
    resources:
      reservations:
        devices:
          - driver: nvidia
            count: 1
            capabilities: [gpu]
```

With GPU, the vectorizer is no longer the bottleneck — the concurrent workers change becomes more valuable (keeps the GPU fed). Estimated combined speedup: **8-15x** vs CPU-only sequential baseline.

### Checkpoint / resume

Add a `--checkpoint` flag (default: `index.checkpoint`) pointing to a file that stores the count of successfully-indexed reviews.

**On startup**: if the file exists, read the number `N`, then fast-forward the scanner by consuming `N` lines before the main batch loop begins. Log `slog.Info("resuming from checkpoint", "offset", N)`.

**After each successful flush**: atomically overwrite the checkpoint file with the new total (write to `*.tmp` then `os.Rename`). This is safe because `BatchUpsert` is idempotent (SHA1 UUIDs), so re-uploading a partial batch on crash is harmless.

**On successful completion**: delete the checkpoint file (or leave it — it's harmless, and the scanner will fast-forward past everything, which is O(scan) but O(0) Weaviate calls).

Add a `--checkpoint ""` (empty string) option to disable checkpointing.

Small helpers in `cli/index.go`:
- `readCheckpoint(path string) (int, error)` — returns 0 if file doesn't exist; use `errors.Is(err, os.ErrNotExist)`
- `writeCheckpoint(path string, n int64) error` — atomic write via temp file + `os.Rename`; no logging before returning errors

Import grouping in `cli/index.go`: stdlib → project (`fodmap/...`) → third-party (`github.com/...`).

## Expected Speedup

| Scenario | Estimate |
|----------|----------|
| CPU only + batch size 500 + 4 workers | 2-3x |
| GPU (ENABLE_CUDA=1) only | 5-10x |
| GPU + batch size 500 + 4 workers | 8-15x |

## Verification
1. `go test ./...` — existing tests pass
2. Run indexing against a live Weaviate instance and observe `slog.Info("indexed batch", "total", ...)` lines appearing in bursts rather than one at a time
3. Compare wall-clock time with `time fodmap-detector index` before and after

## Update: V2 Massively Parallel Pipeline

The initial concurrency improvements focused primarily on Weaviate uploads. However, profiling revealed that the pipeline was still bottlenecked by the sequential scanning and JSON parsing loop.

To achieve maximum 100% saturation on modern hardware (like Apple M2 and desktop CPUs), the pipeline was completely overhauled into a **4-Stage Asynchronous Architecture**:

1. **Stage 0 (Disk Reader)**: The main thread linearly reads from the `.tar` archive as fast as the NVMe drive permits and pumps raw `[]byte` arrays into an enormous channel buffer.
2. **Stage 1 (JSON Parsers)**: `N` independent workers pull from the channel, running `json.Unmarshal` simultaneously to bypass JSON parsing bottlenecks.
3. **Stage 2 (Embedders)**: `N` workers fire HTTP requests to Ollama concurrently. Ollama handles these requests and batches them efficiently on the GPU/CPU.
4. **Stage 3 (Uploaders)**: `N` workers push vectorized batches into Weaviate.

### Safe Rewind Checkpointing

Because batches now finish completely out of order across the independent worker threads, the original atomic counter checkpoint strategy became unsafe. If Batch 3 finished before Batch 1 and the system crashed, Batch 1 would be lost upon resume!

To guarantee zero data loss without introducing complex locking, we implemented **Safe Rewind Checkpointing**. By subtracting the maximum pipeline in-flight buffer size (`8 * numWorkers * batchSize`) from the current successfully completed total, the checkpoint purposefully rewinds the stream. On crash recovery, overlapping patches are securely re-processed, taking advantage of Weaviate's exact-match UUID idempotency to silently overwrite duplicates and ensure nothing is ever skipped.
