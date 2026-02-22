package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"fodmap/data/schemas"
)

// Analyzer is the interface satisfied by LLMClient. Extracted so the server
// can be constructed with a stub in tests.
type Analyzer interface {
	Analyze(ctx context.Context, reviews []schemas.ReviewSchemaS) (string, error)
}

type Server struct {
	store *JobStore
	llm   Analyzer
	port  int
}

type Config struct {
	Port       int
	PromptPath string
}

// New initialises the LLM client and job store. Returns an error if
// GEMINI_API_KEY is unset or the prompt template cannot be parsed.
func New(ctx context.Context, cfg Config) (*Server, error) {
	llm, err := NewLLMClient(ctx, cfg.PromptPath)
	if err != nil {
		return nil, fmt.Errorf("initializing LLM client: %w", err)
	}
	return &Server{
		store: NewJobStore(),
		llm:   llm,
		port:  cfg.Port,
	}, nil
}

// NewServer creates a Server with the provided Analyzer. Intended for tests
// where the real LLM client should not be initialised.
func NewServer(llm Analyzer, port int) *Server {
	return &Server{store: NewJobStore(), llm: llm, port: port}
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /analyze", s.analyzeHandler)
	mux.HandleFunc("GET /results/{job_id}", s.resultsHandler)
	mux.HandleFunc("GET /reviews", s.reviewsHandler)
	return mux
}

// Start registers routes and begins serving HTTP requests.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	slog.Info("server listening", "addr", addr)
	return http.ListenAndServe(addr, s.Handler())
}
