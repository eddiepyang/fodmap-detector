package server

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"text/template"
	"time"

	"fodmap/data/schemas"

	"golang.org/x/time/rate"
	"google.golang.org/genai"
)

const (
	geminiModel = "gemini-2.0-flash"

	// freeTierRPM is the Gemini 2.0 Flash free-tier request rate limit.
	// Source: https://ai.google.dev/gemini-api/docs/rate-limits
	freeTierRPM = 15

	// reviewsPerChunk controls how many reviews are batched into a single API call.
	reviewsPerChunk = 10

	// analysisWorkers is the number of concurrent goroutines making API calls.
	// Throughput is governed by the shared rate limiter, not this number.
	analysisWorkers = 5
)

type LLMClient struct {
	client     *genai.Client
	promptTmpl *template.Template
	limiter    *rate.Limiter
}

// NewLLMClient reads GEMINI_API_KEY from the environment, parses the prompt
// template at promptPath, and initialises a rate limiter at freeTierRPM.
func NewLLMClient(ctx context.Context, promptPath string) (*LLMClient, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY environment variable is not set")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("creating genai client: %w", err)
	}

	tmplBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return nil, fmt.Errorf("reading prompt template %q: %w", promptPath, err)
	}
	tmpl, err := template.New("prompt").Parse(string(tmplBytes))
	if err != nil {
		return nil, fmt.Errorf("parsing prompt template: %w", err)
	}

	return &LLMClient{
		client:     client,
		promptTmpl: tmpl,
		// Allow a burst equal to freeTierRPM so a fresh client can fire
		// up to 15 requests immediately before the token bucket refill kicks in.
		limiter: rate.NewLimiter(rate.Every(time.Minute/freeTierRPM), freeTierRPM),
	}, nil
}

type chunkResult struct {
	index  int
	output string
	err    error
}

// Analyze splits reviews into chunks of reviewsPerChunk and processes them
// concurrently with analysisWorkers goroutines. All goroutines share the same
// rate limiter so the combined request rate never exceeds freeTierRPM.
func (l *LLMClient) Analyze(ctx context.Context, reviews []schemas.ReviewSchemaS) (string, error) {
	chunks := chunkReviews(reviews, reviewsPerChunk)

	workCh := make(chan struct {
		index int
		chunk []schemas.ReviewSchemaS
	}, len(chunks))

	for i, c := range chunks {
		workCh <- struct {
			index int
			chunk []schemas.ReviewSchemaS
		}{i, c}
	}
	close(workCh)

	resultCh := make(chan chunkResult, len(chunks))

	var wg sync.WaitGroup
	for range analysisWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for work := range workCh {
				out, err := l.callGemini(ctx, work.chunk)
				resultCh <- chunkResult{index: work.index, output: out, err: err}
			}
		}()
	}

	wg.Wait()
	close(resultCh)

	results := make([]string, len(chunks))
	for r := range resultCh {
		if r.err != nil {
			return "", fmt.Errorf("chunk %d: %w", r.index, r.err)
		}
		results[r.index] = r.output
	}

	return strings.Join(results, "\n\n---\n\n"), nil
}

// callGemini waits for a rate limiter token then calls the Gemini API with
// the reviews formatted into the prompt template.
func (l *LLMClient) callGemini(ctx context.Context, reviews []schemas.ReviewSchemaS) (string, error) {
	if err := l.limiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limiter: %w", err)
	}

	var sb strings.Builder
	for i, r := range reviews {
		fmt.Fprintf(&sb, "--- Review %d (stars: %.1f) ---\n%s\n\n", i+1, r.Stars, r.Text)
	}

	var buf bytes.Buffer
	if err := l.promptTmpl.Execute(&buf, struct{ Text string }{sb.String()}); err != nil {
		return "", fmt.Errorf("executing prompt template: %w", err)
	}

	resp, err := l.client.Models.GenerateContent(ctx, geminiModel, genai.Text(buf.String()), nil)
	if err != nil {
		return "", fmt.Errorf("gemini GenerateContent: %w", err)
	}
	return resp.Text(), nil
}

func chunkReviews(reviews []schemas.ReviewSchemaS, size int) [][]schemas.ReviewSchemaS {
	var chunks [][]schemas.ReviewSchemaS
	for size < len(reviews) {
		reviews, chunks = reviews[size:], append(chunks, reviews[:size])
	}
	return append(chunks, reviews)
}
