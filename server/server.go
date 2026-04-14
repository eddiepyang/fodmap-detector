package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"fodmap/auth"
	"fodmap/data"
	"fodmap/search"

	"golang.org/x/time/rate"
	"google.golang.org/genai"
)

// Searcher is the interface satisfied by search.Client. Extracted so the server
// can be constructed with a stub in tests.
type Searcher interface {
	GetBusinesses(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchResult, error)
	GetReviews(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchReviews, error)
	SearchFodmap(ctx context.Context, ingredient string) (search.FodmapResult, float64, error)
	EnsureSchema(ctx context.Context) error
	EnsureFodmapSchema(ctx context.Context) error
	BatchUpsertFodmap(ctx context.Context, items map[string]data.FodmapEntry) error
	BatchUpsert(ctx context.Context, items []search.IndexItem) error
}

type Server struct {
	searcher           Searcher // nil when Weaviate is not configured
	port               int
	geminiFactory      GeminiChatFactory // nil when chat is not configured
	geminiApiKey       string            // for manual session creation
	chatModel          string            // for manual session creation
	filterModel        string            // for topic screening
	chatAPIKey         string            // bearer token for /chat route
	chatRateLimiter    *ipRateLimiter
	chatMaxConcurrent  int
	corsAllowedOrigins []string
	genaiClient        *genai.Client
	userStore          auth.Store
	jwtSecret          string
}

type Config struct {
	Port int

	// Search configuration.
	WeaviateHost      string // optional; if empty, Weaviate is not used
	WeaviateScheme    string // optional; e.g. "http" or "https"
	WeaviateAPIKey    string // optional; for Weaviate Cloud (WCD)
	PineconeAPIKey    string // optional
	PineconeIndexHost string // optional (must start with https://)
	VectorizerURL     string // required for Pinecone; optional otherwise
	PostgresSearch    bool   // optional; if true, uses PostgreSQL for search
	PostgresDSN       string // required if PostgresSearch is true

	// Chat endpoint configuration.
	GeminiAPIKey       string  // Gemini API key; omit to disable /chat
	ChatModel          string  // Gemini model ID for chat (default: gemini-3-flash-preview)
	FilterModel        string  // Gemini model ID for filtering (default: gemini-3.1-flash-lite-preview)
	ChatAPIKey         string  // Bearer token clients must present for /chat
	ChatRateLimit      float64 // requests per second per IP (default: 2)
	ChatRateBurst      int     // burst allowance (default: 5)
	ChatMaxConcurrent  int     // max simultaneous chat requests (default: 10)
	CORSAllowedOrigins []string
	UserStore          auth.Store
	JWTSecret          string
}

// New initialises the server and Searcher client.
func New(ctx context.Context, cfg Config) (*Server, error) {
	s := &Server{
		port:               cfg.Port,
		corsAllowedOrigins: cfg.CORSAllowedOrigins,
		userStore:          cfg.UserStore,
		jwtSecret:          cfg.JWTSecret,
	}

	if cfg.PostgresSearch && cfg.PostgresDSN != "" {
		v := search.NewVectorizerClient(cfg.VectorizerURL)
		sc, err := search.NewPostgresClient(cfg.PostgresDSN, v)
		if err != nil {
			return nil, fmt.Errorf("initializing postgres search client: %w", err)
		}
		s.searcher = sc
		slog.Info("postgres (pgvector) search enabled")
	} else if cfg.PineconeAPIKey != "" && cfg.PineconeIndexHost != "" {
		v := search.NewVectorizerClient(cfg.VectorizerURL)
		s.searcher = search.NewPineconeClient(cfg.PineconeAPIKey, cfg.PineconeIndexHost, v)
		slog.Info("pinecone search enabled", "host", cfg.PineconeIndexHost)
	} else if cfg.WeaviateHost != "" {
		sc, err := search.NewClient(cfg.WeaviateHost, cfg.WeaviateScheme, cfg.WeaviateAPIKey)
		if err != nil {
			return nil, fmt.Errorf("initializing weaviate client: %w", err)
		}
		s.searcher = sc
		slog.Info("weaviate search enabled", "host", cfg.WeaviateHost)
	}

	if s.searcher != nil {
		if err := s.searcher.EnsureSchema(ctx); err != nil {
			slog.Warn("ensure yelp schema failed", "error", err)
		}
		if err := s.searcher.EnsureFodmapSchema(ctx); err != nil {
			slog.Warn("ensure fodmap schema failed", "error", err)
		}
		if err := s.searcher.BatchUpsertFodmap(ctx, data.FodmapDB); err != nil {
			slog.Warn("batch upsert fodmap failed", "error", err)
		}
	}

	// Rate limiter and concurrency — used by conversation and chat routes.
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

	// Chat endpoint setup.
	if cfg.GeminiAPIKey != "" && (cfg.ChatAPIKey != "" || cfg.JWTSecret != "") {
		chatModel := cfg.ChatModel
		if chatModel == "" {
			chatModel = "gemini-3-flash-preview"
		}
		filterModel := cfg.FilterModel
		if filterModel == "" {
			filterModel = "gemini-3.1-flash-lite-preview"
		}
		s.geminiFactory = newGeminiChatFactory(cfg.GeminiAPIKey, chatModel)
		s.geminiApiKey = cfg.GeminiAPIKey
		s.chatModel = chatModel
		s.filterModel = filterModel
		s.chatAPIKey = cfg.ChatAPIKey

		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:  cfg.GeminiAPIKey,
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			return nil, fmt.Errorf("creating gemini client: %w", err)
		}
		s.genaiClient = client
		slog.Info("chat endpoint enabled", "model", chatModel)
	}

	return s, nil
}

// NewServer creates a Server with the provided Searcher. Intended for tests
// where the real LLM and Weaviate clients should not be initialised. Pass nil for searcher
// to disable the search endpoint.
func NewServer(searcher Searcher, port int) *Server {
	return &Server{
		searcher:          searcher,
		port:              port,
		jwtSecret:         "test-secret", // default for tests
		userStore:         newMockStore(),
		chatRateLimiter:   newIPRateLimiter(100, 100),
		chatMaxConcurrent: 10,
	}
}

// ChatConfig holds optional chat-related overrides for NewServerWithChat.
type ChatConfig struct {
	GeminiFactory GeminiChatFactory
	ChatAPIKey    string
	RateLimit     rate.Limit
	RateBurst     int
	MaxConcurrent int
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
		genaiClient:       nil, // tests inject their own geminiFactory or mock
		userStore:         newMockStore(),
	}
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/reviews", s.reviewsHandler)
	mux.HandleFunc("GET /api/v1/search/businesses/{query...}", s.getBusinessesHandler)
	mux.HandleFunc("GET /api/v1/search/reviews/{query...}", s.getReviewsHandler)
	mux.HandleFunc("GET /api/v1/search/fodmap/{ingredient...}", s.getFodmapHandler)

	// Auth handlers
	mux.HandleFunc("POST /api/v1/auth/register", s.registerHandler)
	mux.HandleFunc("POST /api/v1/auth/login", s.loginHandler)
	mux.HandleFunc("POST /api/v1/auth/refresh", s.refreshHandler)
	mux.Handle("POST /api/v1/auth/logout", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.logoutHandler)))

	// Conversation handlers (protected by JWT)
	mux.Handle("GET /api/v1/conversations", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.listConversationsHandler)))
	mux.Handle("GET /api/v1/conversations/{id}", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.getConversationHandler)))
	mux.Handle("DELETE /api/v1/conversations/{id}", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.deleteConversationHandler)))

	// Conversation creation (protected by JWT, rate limited)
	createConvMid := chain(
		http.HandlerFunc(s.createConversationHandler),
		jwtAuth(s.jwtSecret),
		rateLimitMiddleware(s.chatRateLimiter),
		concurrencyLimiter(s.chatMaxConcurrent),
	)
	mux.Handle("POST /api/v1/conversations", createConvMid)

	// Chat message stream (protected by JWT/API Key, rate limited)
	postChatMid := chain(
		s.chatHandler(s.genaiClient),
		combinedAuth(s.jwtSecret, s.chatAPIKey),
		rateLimitMiddleware(s.chatRateLimiter),
		concurrencyLimiter(s.chatMaxConcurrent),
	)
	mux.Handle("POST /api/v1/conversations/{id}/messages", postChatMid)

	// Legacy Chat endpoint
	mux.Handle("POST /chat/{query...}", postChatMid)

	return corsMiddleware(s.corsAllowedOrigins)(mux)
}

// Start registers routes and begins serving HTTP requests.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	slog.Info("server listening", "addr", addr)
	return http.ListenAndServe(addr, s.Handler())
}
