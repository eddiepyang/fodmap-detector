package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"fodmap/auth"
	"fodmap/chat"
	"fodmap/data"
	"fodmap/fodmap/store"
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

// FodmapWriter is a narrow capability interface for syncing a single ingredient
// change to the vector search index. Search backends implement this separately
// from Searcher so existing Searcher test stubs stay untouched.
type FodmapWriter interface {
	UpsertFodmapItem(ctx context.Context, name string, entry data.FodmapEntry) error
	DeleteFodmapItem(ctx context.Context, name string) error
}

// CatalogStore is the canonical store for FODMAP ingredient metadata. It is
// implemented by *store.FodmapCatalogStore in production and by in-memory
// stubs in tests.
type CatalogStore interface {
	EnsureSchema(ctx context.Context) error
	Create(ctx context.Context, entry store.CatalogEntry) error
	Get(ctx context.Context, name string) (*store.CatalogEntry, error)
	List(ctx context.Context, offset, limit int, filter store.ListFilter) ([]store.CatalogEntry, error)
	Count(ctx context.Context, filter store.ListFilter) (int, error)
	Stats(ctx context.Context) (*store.Stats, error)
	Update(ctx context.Context, name string, entry store.CatalogEntry) error
	Delete(ctx context.Context, name string) error
	ListAll(ctx context.Context) ([]store.CatalogEntry, error)
	IsSeeded(ctx context.Context) (bool, error)
	SetSeeded(ctx context.Context) error
	Seed(ctx context.Context, items map[string]data.FodmapEntry) error
	Reseed(ctx context.Context, items map[string]data.FodmapEntry) (int, error)
	Close() error
}

// MenuStore manages the RestaurantMenu collection. It is defined separately
// from Searcher so the same Weaviate/Postgres/Pinecone backend types can
// satisfy both interfaces independently. DeleteStaleMenu is deferred to a
// follow-up PR (YAGNI — no --purge-stale flag yet).
type MenuStore interface {
	EnsureMenuSchema(ctx context.Context) error
	BatchUpsertMenu(ctx context.Context, items []search.MenuItem) error
	SearchMenu(ctx context.Context, query string, limit int) ([]search.MenuItem, error)
}

type Server struct {
	searcher           Searcher // nil when Weaviate is not configured
	catalogStore       CatalogStore
	port               int
	chatBackend        chat.ChatBackend // nil when chat is not configured
	geminiApiKey       string           // for manual session creation
	chatModel          string           // for manual session creation
	filterModel        string           // for topic screening
	chatAPIKey         string           // bearer token for /chat route
	chatRateLimiter    *ipRateLimiter
	chatMaxConcurrent  int
	corsAllowedOrigins []string
	genaiClient        *genai.Client
	userStore          auth.AdminStore
	jwtSecret          string
	adminEmail         string
	menutrackingAdmin  http.Handler // nil when menutracking is not configured
}

type Config struct {
	Port int

	// Search configuration.
	WeaviateHost      string          // optional; if empty, Weaviate is not used
	WeaviateScheme    string          // optional; e.g. "http" or "https"
	WeaviateAPIKey    string          // optional; for Weaviate Cloud (WCD)
	PineconeAPIKey    string          // optional
	PineconeIndexHost string          // optional (must start with https://)
	VectorizerURL     string          // required for Pinecone; optional otherwise
	PostgresSearch    bool            // optional; if true, uses PostgreSQL for search
	PostgresDSN       string          // required; used by both auth and the FODMAP catalog store
	Embedder          search.Embedder // embedding provider (LlamaEmbedder or VectorizerClient)
	CatalogStore      CatalogStore    // canonical FODMAP ingredient store

	// Chat endpoint configuration.
	GeminiAPIKey       string  // Gemini API key; omit to disable /chat
	ChatModel          string  // Gemini model ID for chat (default: gemini-3-flash-preview)
	FilterModel        string  // Gemini model ID for filtering (default: gemini-3.1-flash-lite-preview)
	ChatAPIKey         string  // Bearer token clients must present for /chat
	ChatRateLimit      float64 // requests per second per IP (default: 2)
	ChatRateBurst      int     // burst allowance (default: 5)
	ChatMaxConcurrent  int     // max simultaneous chat requests (default: 10)
	CORSAllowedOrigins []string
	UserStore          auth.AdminStore
	JWTSecret          string
	AdminEmail         string
	MenutrackingAdmin  http.Handler // nil when menutracking is not configured
}

// New initialises the server and Searcher client.
func New(ctx context.Context, cfg Config) (*Server, error) {
	s := &Server{
		port:               cfg.Port,
		corsAllowedOrigins: cfg.CORSAllowedOrigins,
		userStore:          cfg.UserStore,
		catalogStore:       cfg.CatalogStore,
		jwtSecret:          cfg.JWTSecret,
		adminEmail:         cfg.AdminEmail,
		menutrackingAdmin:  cfg.MenutrackingAdmin,
	}

	if cfg.PostgresSearch && cfg.PostgresDSN != "" {
		sc, err := search.NewPostgresClient(cfg.PostgresDSN, cfg.Embedder)
		if err != nil {
			return nil, fmt.Errorf("initializing postgres search client: %w", err)
		}
		s.searcher = sc
		slog.Info("postgres (pgvector) search enabled")
	} else if cfg.PineconeAPIKey != "" && cfg.PineconeIndexHost != "" {
		s.searcher = search.NewPineconeClient(cfg.PineconeAPIKey, cfg.PineconeIndexHost, cfg.Embedder)
		slog.Info("pinecone search enabled", "host", cfg.PineconeIndexHost)
	} else if cfg.WeaviateHost != "" {
		sc, err := search.NewClient(cfg.WeaviateHost, cfg.WeaviateScheme, cfg.WeaviateAPIKey, cfg.Embedder)
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
	}

	if err := s.seedAndReload(ctx); err != nil {
		slog.Warn("fodmap seed/reload failed", "error", err)
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
		s.chatBackend = chat.NewGeminiBackend(client, chatModel)
		slog.Info("chat endpoint enabled", "model", chatModel)
	}

	return s, nil
}

// seedAndReload seeds the canonical catalog once from the static map and
// refreshes the vector search index from the catalog so edits survive restarts.
func (s *Server) seedAndReload(ctx context.Context) error {
	if s.catalogStore == nil {
		if s.searcher != nil {
			// Legacy path: no catalog store yet, fall back to the static map so
			// existing deployments keep working until the catalog store is wired.
			return s.searcher.BatchUpsertFodmap(ctx, data.FodmapDB)
		}
		return nil
	}

	if err := s.catalogStore.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("ensure catalog schema: %w", err)
	}

	seeded, err := s.catalogStore.IsSeeded(ctx)
	if err != nil {
		return fmt.Errorf("checking seeded marker: %w", err)
	}
	if !seeded {
		if err := s.catalogStore.Seed(ctx, data.FodmapDB); err != nil {
			return fmt.Errorf("seeding catalog: %w", err)
		}
		slog.Info("seeded fodmap catalog", "count", len(data.FodmapDB))
	}

	if s.searcher == nil {
		return nil
	}

	start := time.Now()
	items, err := s.catalogStore.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("listing catalog for reload: %w", err)
	}
	if err := s.searcher.BatchUpsertFodmap(ctx, store.ToMap(items)); err != nil {
		return fmt.Errorf("reloading vector index: %w", err)
	}
	slog.Info("reloaded fodmap vector index from catalog", "count", len(items), "duration", time.Since(start))
	return nil
}

// NewServer creates a Server with the provided Searcher. Intended for tests
// where the real LLM and Weaviate clients should not be initialised. Pass nil for searcher
// to disable the search endpoint.
func NewServer(searcher Searcher, port int) *Server {
	return &Server{
		searcher:          searcher,
		catalogStore:      newInMemoryCatalogStore(),
		port:              port,
		jwtSecret:         "test-secret", // default for tests
		userStore:         newMockStore(),
		chatRateLimiter:   newIPRateLimiter(100, 100),
		chatMaxConcurrent: 10,
	}
}

// ChatConfig holds optional chat-related overrides for NewServerWithChat.
type ChatConfig struct {
	Backend       chat.ChatBackend
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
		catalogStore:      newInMemoryCatalogStore(),
		port:              port,
		chatBackend:       cfg.Backend,
		chatAPIKey:        cfg.ChatAPIKey,
		chatRateLimiter:   newIPRateLimiter(rl, burst),
		chatMaxConcurrent: maxConc,
		genaiClient:       nil, // tests inject their own backend or mock
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
	mux.Handle("GET /api/v1/auth/me", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.meHandler)))
	mux.Handle("POST /api/v1/auth/logout", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.logoutHandler)))
	mux.Handle("DELETE /api/v1/auth/user", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.deleteUserHandler)))

	// Admin endpoints (JWT -> adminRequired middleware -> handler)
	adminMid := func(h http.HandlerFunc) http.Handler {
		return chain(http.HandlerFunc(h), jwtAuth(s.jwtSecret), s.adminRequired)
	}
	mux.Handle("GET /api/v1/admin/users", adminMid(s.adminListUsersHandler))
	mux.Handle("GET /api/v1/admin/users/{id}", adminMid(s.adminGetUserHandler))
	mux.Handle("PUT /api/v1/admin/users/{id}/status", adminMid(s.adminUpdateUserStatusHandler))
	mux.Handle("DELETE /api/v1/admin/users/{id}", adminMid(s.adminDeleteUserHandler))
	mux.Handle("POST /api/v1/admin/users/{id}/reset-password", adminMid(s.adminResetPasswordHandler))
	mux.Handle("GET /api/v1/admin/conversations", adminMid(s.adminListConversationsHandler))
	mux.Handle("GET /api/v1/admin/conversations/{id}", adminMid(s.adminGetConversationHandler))
	mux.Handle("GET /api/v1/admin/ingredients", adminMid(s.adminListIngredientsHandler))
	mux.Handle("GET /api/v1/admin/ingredients/stats", adminMid(s.adminIngredientStatsHandler))
	mux.Handle("GET /api/v1/admin/ingredients/search-test", adminMid(s.adminIngredientSearchTestHandler))
	mux.Handle("GET /api/v1/admin/ingredients/{name}", adminMid(s.adminGetIngredientHandler))
	mux.Handle("POST /api/v1/admin/ingredients", adminMid(s.adminCreateIngredientHandler))
	mux.Handle("PUT /api/v1/admin/ingredients/{name}", adminMid(s.adminUpdateIngredientHandler))
	mux.Handle("DELETE /api/v1/admin/ingredients/{name}", adminMid(s.adminDeleteIngredientHandler))
	mux.Handle("POST /api/v1/admin/ingredients/reseed", adminMid(s.adminReseedIngredientsHandler))
	mux.Handle("GET /api/v1/admin/analytics/overview", adminMid(s.adminAnalyticsOverviewHandler))
	mux.Handle("GET /api/v1/admin/analytics/activity", adminMid(s.adminConversationActivityHandler))

	// Conversation handlers (protected by JWT)
	mux.Handle("GET /api/v1/conversations", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.listConversationsHandler)))
	mux.Handle("GET /api/v1/conversations/{id}", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.getConversationHandler)))
	mux.Handle("DELETE /api/v1/conversations/{id}", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.deleteConversationHandler)))
	mux.Handle("GET /api/v1/conversations/{id}/export", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.exportConversationHandler)))

	// Conversation creation (protected by JWT, rate limited)
	createConvMid := chain(
		http.HandlerFunc(s.createConversationHandler),
		jwtAuth(s.jwtSecret),
		rateLimitMiddleware(s.chatRateLimiter),
		concurrencyLimiter(s.chatMaxConcurrent),
	)
	mux.Handle("POST /api/v1/conversations", createConvMid)

	// User Profile
	profileMid := chain(
		http.HandlerFunc(s.updateProfileHandler),
		jwtAuth(s.jwtSecret),
		rateLimitMiddleware(s.chatRateLimiter),
		concurrencyLimiter(s.chatMaxConcurrent),
	)
	mux.Handle("POST /api/v1/profile", profileMid)
	mux.Handle("GET /api/v1/profile", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.getProfileHandler)))

	// Chat endpoint (protected by JWT, rate limited)
	if s.chatBackend != nil {
		chatMid := chain(
			s.chatHandler(s.chatBackend),
			combinedAuth(s.jwtSecret, s.chatAPIKey),
			rateLimitMiddleware(s.chatRateLimiter),
			concurrencyLimiter(s.chatMaxConcurrent),
		)
		mux.Handle("POST /chat/{query...}", chatMid)
		mux.Handle("POST /api/v1/chat/{query...}", chatMid)
		mux.Handle("POST /api/v1/conversations/{id}/messages", chatMid)
	}

	// Menutracking admin endpoints (protected by JWT or ChatAPIKey).
	if s.menutrackingAdmin != nil {
		adminMid := chain(
			s.menutrackingAdmin,
			combinedAuth(s.jwtSecret, s.chatAPIKey),
		)
		mux.Handle("GET /menutracking/sources", adminMid)
		mux.Handle("GET /menutracking/jobs", adminMid)
		mux.Handle("POST /menutracking/reload", adminMid)
	}
	return corsMiddleware(s.corsAllowedOrigins)(mux)
}

// ChatBackend returns the chat backend configured for this server, or nil if
// chat is not enabled. Exported so the CLI can pass it to other subsystems
// (e.g. the menutracking pipeline).
func (s *Server) ChatBackend() chat.ChatBackend {
	return s.chatBackend
}

// SetMenutrackingAdmin sets the menutracking admin handler. Called after pipeline
// startup to wire the admin endpoints.
func (s *Server) SetMenutrackingAdmin(h http.Handler) {
	s.menutrackingAdmin = h
}

// Searcher returns the underlying search client, or nil if search is not
// enabled. The return type is the concrete interface so callers can type-assert
// to access additional methods.
func (s *Server) Searcher() Searcher {
	return s.searcher
}

// Start registers routes and begins serving HTTP requests.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	slog.Info("server listening", "addr", addr)
	return http.ListenAndServe(addr, s.Handler())
}
