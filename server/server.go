package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"fodmap/data/schemas"
	"fodmap/search"
)

// Analyzer is the interface satisfied by LLMClient. Extracted so the server
// can be constructed with a stub in tests.
type Analyzer interface {
	Analyze(ctx context.Context, reviews []schemas.Review) (string, error)
}

// Searcher is the interface satisfied by search.Client. Extracted so the server
// can be constructed with a stub in tests.
type Searcher interface {
	GetBusinesses(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchResult, error)
	GetReviews(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchReviews, error)
}

type Server struct {
	store    *JobStore
	llm      Analyzer
	searcher Searcher // nil when Weaviate is not configured
	port     int
}

type Config struct {
	Port         int
	PromptPath   string
	WeaviateHost string // optional; if empty, the search endpoint returns 503
}

// New initialises the LLM client and job store. Returns an error if
// GEMINI_API_KEY is unset or the prompt template cannot be parsed.
func New(ctx context.Context, cfg Config) (*Server, error) {
	llm, err := NewLLMClient(ctx, cfg.PromptPath)
	if err != nil {
		return nil, fmt.Errorf("initializing LLM client: %w", err)
	}

	s := &Server{
		store: NewJobStore(),
		llm:   llm,
		port:  cfg.Port,
	}

	if cfg.WeaviateHost != "" {
		sc, err := search.NewClient(cfg.WeaviateHost)
		if err != nil {
			return nil, fmt.Errorf("initializing weaviate client: %w", err)
		}
		s.searcher = sc
		slog.Info("weaviate search enabled", "host", cfg.WeaviateHost)
	}

	return s, nil
}

// NewServer creates a Server with the provided Analyzer and Searcher. Intended for tests
// where the real LLM and Weaviate clients should not be initialised. Pass nil for searcher
// to disable the search endpoint.
func NewServer(llm Analyzer, searcher Searcher, port int) *Server {
	return &Server{store: NewJobStore(), llm: llm, searcher: searcher, port: port}
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /analyze", s.analyzeHandler)
	mux.HandleFunc("GET /results/{job_id}", s.resultsHandler)
	mux.HandleFunc("GET /reviews", s.reviewsHandler)
	mux.HandleFunc("GET /searchBusiness/{query...}", s.getBusinessesHandler)
	mux.HandleFunc("GET /searchReview/{query...}", s.getReviewsHandler)
	return mux
}

// Start registers routes and begins serving HTTP requests.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	slog.Info("server listening", "addr", addr)
	return http.ListenAndServe(addr, s.Handler())
}
