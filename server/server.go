package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"fodmap/data"
	"fodmap/search"

	"golang.org/x/time/rate"
)

// Searcher is the interface satisfied by search.Client. Extracted so the server
// can be constructed with a stub in tests.
type Searcher interface {
	GetBusinesses(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchResult, error)
	GetReviews(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchReviews, error)
	SearchFodmap(ctx context.Context, ingredient string) (search.FodmapResult, float64, error)
}

type Server struct {
	searcher          Searcher           // nil when Weaviate is not configured
	port              int
	geminiFactory     GeminiChatFactory  // nil when chat is not configured
	geminiApiKey      string             // for manual session creation
	geminiModel       string             // for manual session creation
	chatAPIKey        string             // bearer token for /chat route
	chatRateLimiter   *ipRateLimiter
	chatMaxConcurrent int
}

type Config struct {
	Port int

	WeaviateHost string // optional; if empty, the search endpoint returns 503

	// Chat endpoint configuration.
	GeminiAPIKey      string  // Gemini API key; omit to disable /chat
	GeminiModel       string  // Gemini model ID (default: gemini-3-flash-preview)
	ChatAPIKey        string  // Bearer token clients must present for /chat
	ChatRateLimit     float64 // requests per second per IP (default: 2)
	ChatRateBurst     int     // burst allowance (default: 5)
	ChatMaxConcurrent int     // max simultaneous chat requests (default: 10)
}

// New initialises the server and Searcher client.
func New(ctx context.Context, cfg Config) (*Server, error) {
	s := &Server{
		port: cfg.Port,
	}

	if cfg.WeaviateHost != "" {
		sc, err := search.NewClient(cfg.WeaviateHost)
		if err != nil {
			return nil, fmt.Errorf("initializing weaviate client: %w", err)
		}
		s.searcher = sc
		slog.Info("weaviate search enabled", "host", cfg.WeaviateHost)

		if err := sc.EnsureFodmapSchema(ctx); err != nil {
			slog.Warn("ensuring fodmap schema failed", "error", err)
		} else {
			if err := sc.BatchUpsertFodmap(ctx, data.FodmapDB); err != nil {
				slog.Warn("batch upsert fodmap failed", "error", err)
			}
		}
	}

	// Chat endpoint setup.
	if cfg.GeminiAPIKey != "" && cfg.ChatAPIKey != "" {
		model := cfg.GeminiModel
		if model == "" {
			model = "gemini-3-flash-preview"
		}
		s.geminiFactory = newGeminiChatFactory(cfg.GeminiAPIKey, model)
		s.geminiApiKey = cfg.GeminiAPIKey
		s.geminiModel = model
		s.chatAPIKey = cfg.ChatAPIKey

		rl := cfg.ChatRateLimit
		if rl <= 0 {
			rl = 2
		}
		burst := cfg.ChatRateBurst
		if burst <= 0 {
			burst = 5
		}
		s.chatRateLimiter = newIPRateLimiter(rate.Limit(rl), burst)

		s.chatMaxConcurrent = cfg.ChatMaxConcurrent
		if s.chatMaxConcurrent <= 0 {
			s.chatMaxConcurrent = 10
		}
		slog.Info("chat endpoint enabled", "model", model)
	}

	return s, nil
}

// NewServer creates a Server with the provided Searcher. Intended for tests
// where the real LLM and Weaviate clients should not be initialised. Pass nil for searcher
// to disable the search endpoint.
func NewServer(searcher Searcher, port int) *Server {
	return &Server{searcher: searcher, port: port}
}

// ChatConfig holds optional chat-related overrides for NewServerWithChat.
type ChatConfig struct {
	GeminiFactory  GeminiChatFactory
	ChatAPIKey     string
	RateLimit      rate.Limit
	RateBurst      int
	MaxConcurrent  int
}

// NewServerWithChat creates a Server with chat endpoint support. Intended for tests.
func NewServerWithChat(searcher Searcher, port int, cfg ChatConfig) *Server {
	rl := cfg.RateLimit
	if rl <= 0 {
		rl = 100 // generous for tests
	}
	burst := cfg.RateBurst
	if burst <= 0 {
		burst = 100
	}
	maxConc := cfg.MaxConcurrent
	if maxConc <= 0 {
		maxConc = 10
	}
	return &Server{
		searcher:          searcher,
		port:              port,
		geminiFactory:     cfg.GeminiFactory,
		chatAPIKey:        cfg.ChatAPIKey,
		chatRateLimiter:   newIPRateLimiter(rl, burst),
		chatMaxConcurrent: maxConc,
	}
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /reviews", s.reviewsHandler)
	mux.HandleFunc("GET /searchBusiness/{query...}", s.getBusinessesHandler)
	mux.HandleFunc("GET /searchReview/{query...}", s.getReviewsHandler)
	mux.HandleFunc("GET /searchFodmap/{ingredient...}", s.getFodmapHandler)

	// Chat endpoint with auth, rate limiting, and concurrency control.
	if s.geminiFactory != nil && s.chatAPIKey != "" {
		chatHandler := chain(
			http.HandlerFunc(s.chatHandler),
			bearerAuth(s.chatAPIKey),
			rateLimitMiddleware(s.chatRateLimiter),
			concurrencyLimiter(s.chatMaxConcurrent),
		)
		mux.Handle("POST /chat/{query...}", chatHandler)
	}

	return mux
}

// Start registers routes and begins serving HTTP requests.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	slog.Info("server listening", "addr", addr)
	return http.ListenAndServe(addr, s.Handler())
}
