# Project Structure

The codebase is organized into several key packages and sub-systems.

```
.
├── main.go                  # Server entry point
├── cli/
│   ├── root.go              # Root Cobra command
│   ├── serve.go             # Serve subcommand (starts the HTTP server)
│   ├── index.go             # Index subcommand (populates vector store for search)
│   ├── scrape.go            # Scrape subcommand (menu extraction and indexing)
│   ├── chat.go              # Chat subcommand (interactive FODMAP/allergen agent)
│   └── event.go             # Avro subcommand (event write / event read)
│
├── chat/
│   ├── chat.go              # Chat session logic, tool dispatch, system prompt rendering
│   ├── backend.go           # Provider-agnostic ChatBackend interface (ToolDeclaration, Message, GenerateOpts)
│   ├── gemini_backend.go    # Gemini implementation of ChatBackend (genai SDK)
│   ├── openai_backend.go    # OpenAI-compatible implementation of ChatBackend (Ollama, vLLM, OpenAI)
│   ├── profile.go           # Dietary profile generation via Gemini
│   ├── chat-instruction.txt # Embedded instruction template for the chat agent
│   └── dietary-profile-prompt.txt # Embedded prompt template for dietary profile generation
│
├── server/
│   ├── server.go            # HTTP server setup and routes
│   ├── handlers.go          # Search & FODMAP HTTP handlers
│   ├── auth_handler.go      # Auth endpoints (register, login, refresh, delete)
│   ├── admin_handler.go     # Admin Console RBAC endpoints
│   ├── admin_ingredients_handler.go  # Admin FODMAP ingredient CRUD + reseed endpoints
│   ├── catalog_store.go     # In-memory catalog store adapter for ingredient admin
│   ├── chat_handler.go      # Chat streaming handler (SSE)
│   ├── conversation_handler.go       # Conversation CRUD endpoints
│   ├── conversation_export_handler.go # Conversation export (JSON/Markdown)
│   ├── create_conversation.go        # Conversation creation + review summary
│   ├── direct_fodmap_client.go       # Direct FODMAP lookup client for chat
│   ├── profile_handler.go             # Dietary profile endpoints
│   ├── middleware.go         # JWT auth, rate limiting, CORS middleware
│   └── mock_store.go         # In-memory test store
│
├── fodmap/
│   └── store/
│       ├── postgres.go      # PostgreSQL-backed FODMAP ingredient store (CRUD + search)
│       └── sql/             # Embedded SQL queries for the ingredient store
│
├── menutracking/            # Regulatory tracking pipeline (menu change detection + alerting)
│   ├── agent.go             # Tracking agent orchestration
│   ├── workers.go           # Concurrent worker pool
│   ├── rule.go              # Rule evaluation engine
│   ├── fastpath.go          # Fast-path heuristics for common cases
│   ├── schema.go            # Data models for tracking events
│   ├── source.go            # Menu source adapters
│   ├── ratelimit.go         # Per-source rate limiting
│   └── admin.go             # Admin interface for tracking rules
│
├── data/
│   ├── data.go              # Archive reading (TAR + JSON lines)
│   ├── fodmap.go            # Static FODMAP ingredient database (100+ entries)
│   │
│   ├── io/
│   │   └── event.go         # Avro OCF read/write helpers
│   │
│   └── schemas/
│       └── schemas.go       # Review + Business structs + Avro EventSchema
│
├── search/
│   ├── weaviate.go          # Weaviate client: schema, batch upsert, nearText/hybrid search
│   ├── pinecone.go          # Pinecone client: REST-based query, upsert, BM25 re-ranking
│   ├── postgres.go           # PostgreSQL/pgvector client: vector search via SQL
│   ├── bm25.go              # BM25 keyword scoring and score blending for hybrid search
│   ├── embedder.go           # Embedder interface
│   ├── embedder_ollama.go   # Go client for Ollama embeddings API
│   └── vectorizer.go        # HTTP vectorizer proxy client
│
├── auth/
│   ├── store.go             # Unified ChatStore interface
│   ├── admin.go             # AdminStore interface (extends ChatStore)
│   ├── postgres_store.go    # PostgreSQL implementation
│   ├── conversation.go      # Conversation & Message models
│   ├── jwt.go               # Token generation/validation
│   ├── user.go              # User model
│   └── sql/                 # Embedded SQL query files
│
├── internal/
│   └── db/
│       ├── migrate.go       # Centralised migration runner (golang-migrate)
│       └── migrations/      # Versioned .sql migration files
│
├── docs/
│   ├── guides/                  # Playbooks, API/CLI references, system design
│   └── plans/                   # Feature implementation plans and roadmaps
│
└── docker-compose.yaml      # Vector database (Weaviate) + relational DB (PostgreSQL) configuration
```
