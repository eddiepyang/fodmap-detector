# Plan: Add HTTP Server with Async FODMAP Analysis

## Context
The project is a Go CLI tool that reads Yelp reviews from a tar.gz archive and prepares them for FODMAP/allergy analysis. We added:
- An HTTP server subcommand (`fodmap-detector serve`)
- A `GET /reviews?business_id=xxx` endpoint to retrieve raw reviews
- A `POST /analyze?business_id=xxx` endpoint that kicks off an async LLM analysis job
- A `GET /results/{job_id}` endpoint to poll job status and retrieve the result
- Google Gemini 2.0 Flash integration with concurrent workers and rate limiting per the [Gemini API docs](https://ai.google.dev/gemini-api/docs/rate-limits)

---

## Status: Complete ✅

All files created/modified. Build passes (`go build ./...`).

---

## Files Changed

| File | Status | Description |
|------|--------|-------------|
| `server/jobs.go` | ✅ created | In-memory job store (`JobStore`, `Job`, status lifecycle) |
| `server/llm.go` | ✅ created | Gemini client with concurrent workers + 15 RPM rate limiter |
| `server/handlers.go` | ✅ created | HTTP handlers: `analyzeHandler`, `resultsHandler`, `reviewsHandler` |
| `server/server.go` | ✅ created | `Server` struct, routing, `Start()` |
| `cli/serve.go` | ✅ created | Cobra `serve` subcommand (`--port`, `--prompt` flags) |
| `data/data.go` | ✅ modified | Added `GetReviewsByBusiness(businessID string)` |
| `go.mod` + `vendor/` | ✅ modified | Added `google.golang.org/genai v1.47.0`, `golang.org/x/time v0.14.0` |

---

## Architecture

### LLM Concurrency & Rate Limiting (`server/llm.go`)

Reviews for a business are split into chunks of 10 (`reviewsPerChunk`) and
processed concurrently by 5 goroutines (`analysisWorkers`). All goroutines share
a single `*rate.Limiter` on `LLMClient`, keeping the combined call rate at or
below **15 RPM** — the Gemini 2.0 Flash free-tier limit per the docs.

```
reviews ──► chunkReviews() ──► workCh
                                  │
              ┌───────────────────┼───────────────────┐
              ▼                   ▼                   ▼
          worker 1            worker 2  ...       worker 5
              │                   │                   │
              └─────── limiter.Wait(ctx) ─────────────┘
                                  │
                        callGemini() → Gemini API
                                  │
                              resultCh
                                  │
                        strings.Join(results)
```

Key constants in `server/llm.go`:
```go
freeTierRPM      = 15  // Gemini 2.0 Flash free tier (docs.google.dev/gemini-api/docs/rate-limits)
reviewsPerChunk  = 10  // reviews batched per API call
analysisWorkers  = 5   // concurrent goroutines
```

Rate limiter initialisation:
```go
rate.NewLimiter(rate.Every(time.Minute/freeTierRPM), freeTierRPM)
// → 1 token every 4 seconds, burst capacity of 15
```

### Async Job Lifecycle (`server/jobs.go`, `server/handlers.go`)

```
POST /analyze?business_id=xxx
  │
  ├─ JobStore.Create()  → status: pending
  ├─ go runAnalysis()   → status: running
  └─ 202 {"job_id": "..."}

runAnalysis goroutine:
  ├─ data.GetReviewsByBusiness()   (streams archive, filters by business_id)
  ├─ llm.Analyze()                 (chunks → workers → rate-limited Gemini calls)
  └─ JobStore.Update()             → status: complete / failed

GET /results/{job_id}
  └─ JobStore.Get() → full Job JSON (pending / running / complete / failed)
```

`runAnalysis` uses `context.Background()` so jobs complete even if the HTTP
client disconnects before polling.

### Data Layer (`data/data.go`)

`GetReviewsByBusiness` streams the tar.gz archive, decodes each JSON line, and
returns only reviews matching `businessID`. Uses a 4 MB scanner buffer to handle
long review text fields.

---

## API Summary

| Method | Path | Response |
|--------|------|----------|
| `POST` | `/analyze?business_id=xxx` | 202 `{"job_id": "..."}` |
| `GET` | `/results/{job_id}` | 200 `Job` JSON or 404 |
| `GET` | `/reviews?business_id=xxx` | 200 `[]ReviewSchemaS` JSON |

**Job status flow:** `pending` → `running` → `complete` / `failed`

---

## Usage

```bash
# Build
go build -o fodmap-detector .

# Run server (requires GEMINI_API_KEY)
export GEMINI_API_KEY=your_key_here
./fodmap-detector serve --port 8080

# Kick off analysis job
curl -X POST "http://localhost:8080/analyze?business_id=<id>"
# → {"job_id":"a3f1e2..."}

# Poll for result
curl "http://localhost:8080/results/a3f1e2..."
# → {"job_id":"...","status":"complete","result":"...","created_at":"..."}

# Fetch raw reviews
curl "http://localhost:8080/reviews?business_id=<id>"
# → [{"review_id":"...","text":"..."}]
```
