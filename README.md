# FODMAP Detector

A full-stack application (Go HTTP API backend and React SPA frontend) featuring an AI-powered chat assistant that analyzes restaurant menus and Yelp reviews to identify FODMAP (Fermentable Oligosaccharides, Disaccharides, Monosaccharides, and Polyols) content and food allergens. Designed to help individuals with digestive sensitivities make safe dining choices using personalized dietary profiles, hybrid vector-grounded search, and a comprehensive administrative RBAC console.

---

## Purpose

1. Read Yelp review data from a TAR archive of JSON lines
2. Index reviews into a vector search backend (Weaviate, Pinecone, or PostgreSQL/pgvector) for semantic search
3. Provide an interactive semantic search and chat agent for FODMAP/allergen queries

---

## Tech Stack

| Component      | Technology                                                                                                                                       |
| ----------------| --------------------------------------------------------------------------------------------------------------------------------------------------|
| Language       | Go 1.26+                                                                                                                                         |
| CLI            | [Cobra](https://github.com/spf13/cobra)                                                                                                          |
| Input          | TAR archive of JSON lines (Yelp dataset)                                                                                                         |
| Concurrency    | Go channels + goroutines                                                                                                                         |
| Vector search  | [Weaviate](https://weaviate.io) (local), [Pinecone](https://pinecone.io) (cloud), or [PostgreSQL/pgvector](https://github.com/pgvector/pgvector) |
| LLM (chat)     | Google Gemini (`gemini-3-flash-preview`)                                                                                                         |
| LLM (scraping) | Any OpenAI-compatible endpoint (Ollama, vLLM, OpenAI, Gemini)                                                                                    |
| Embeddings     | Ollama (`nomic-embed-text`)                                                                                                                      |

---

## Project Structure

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

---

### Relational Database Backend
> [!IMPORTANT]
> **PostgreSQL is now the mandatory database backend.** SQLite support has been completely decommissioned.
> To run the server, `POSTGRES_DSN` is required (or starts PostgreSQL in Docker via `make start`). Schema migrations are managed by `golang-migrate` and embedded into the binary via `//go:embed`. Run `go run . db migrate-up` before starting the server (or use `./start.sh` which does this automatically).

### Role-Based Access Control (RBAC) & Admin Console
- **RBAC Model**: Users are mapped to `'user'` or `'admin'` roles. The role is claim-based in JWT for client routing, but re-verified against the database on every admin API request for server security.
- **Admin CLI Flag**: The `--admin-email` (or `ADMIN_EMAIL` env var) flag promotes a specific registered user to the `admin` role on startup.
- **API Endpoints (`/api/v1/admin/*`)**:
  - `GET /api/v1/admin/users` - Lists active/suspended users.
  - `GET /api/v1/admin/users/{id}` - Returns user details, message counts, and saved dietary profile.
  - `PUT /api/v1/admin/users/{id}/status` - Toggles user account status (`active` / `suspended`).
  - `DELETE /api/v1/admin/users/{id}` - Performs permanent cascading delete of user's profile, conversations, and messages.
  - `POST /api/v1/admin/users/{id}/reset-password` - Resets password to a secure temporary bcrypt hash.
  - `GET /api/v1/admin/conversations` - Lists user chat sessions.
  - `GET /api/v1/admin/conversations/{id}` - Reads complete transcript messages.
  - `GET /api/v1/admin/analytics/overview` - Fetches total, active, and suspended user counts and signups.
  - `GET /api/v1/admin/analytics/activity` - Fetches daily conversation volume.
- **Ingredient Admin (`/api/v1/admin/ingredients/*`)**:
  - `GET /api/v1/admin/ingredients` - Lists ingredients with optional filters and pagination.
  - `GET /api/v1/admin/ingredients/stats` - Returns aggregate counts by FODMAP level and group.
  - `GET /api/v1/admin/ingredients/search-test` - Runs a semantic search against the ingredient catalog.
  - `GET /api/v1/admin/ingredients/{name}` - Returns a single ingredient by name.
  - `POST /api/v1/admin/ingredients` - Creates a new ingredient (rejects duplicates).
  - `PUT /api/v1/admin/ingredients/{name}` - Updates an existing ingredient.
  - `DELETE /api/v1/admin/ingredients/{name}` - Deletes an ingredient from the catalog.
  - `POST /api/v1/admin/ingredients/reseed` - Re-upserts the static FODMAP database into the store.

---

## Documentation

We have split our documentation into separate guides and plans for easier reading.

### Guides & Playbooks
- [Quick Start & Setup](docs/guides/quick-start.md) - How to run the infrastructure and project locally.
- [API Reference](docs/guides/api-reference.md) - HTTP endpoints, auth, and search.
- [CLI Reference](docs/guides/cli-reference.md) - Commands for indexing, scraping, and chatting.
- [Data Model & Pipeline](docs/guides/data-model.md) - Core data structures and inputs.
- [Database Schema](docs/guides/database-schema.md) - Postgres table reference and migration guide.
- [Testing](docs/guides/testing.md) - How to run tests and coverage targets.
- [Search Design](docs/guides/search.md) - Search service design decisions.
- [Chat Design](docs/guides/chat.md) - Chat agent design decisions.
- [Server Design](docs/guides/server.md) - Server design and API docs.
- [LLM Serving](docs/guides/llm-serving.md) - LLM serving architectures.
- [Troubleshooting & Diagnostics](docs/guides/troubleshooting.md) - Common issues, Weaviate diagnostic queries, and schema migration steps.

### Plans & Roadmaps

**Completed:**
- [Admin System & RBAC Console Plan](docs/plans/admin-page-plan.md)
- [Scraper Pipeline Plan](docs/plans/scraper-pipeline-plan.md)
- [Dietary Profile Plan](docs/plans/dietary-profile-plan.md)
- [Indexing Improvements](docs/plans/indexing-improvements.md)
- [Deleted User Plan](docs/plans/handle-deleted-user-plan.md)
- [Frontend Plan](docs/plans/frontend-plan.md)
- [SQL File Management Plan](docs/plans/sql-file-management-plan.md)

**In Progress:**
- [Scraper Service Integration Plan](docs/plans/scraper-service-integration-plan.md) — routing PDF/OCR, image-embedded menus, and JS-rendered pages to the Python `scraper` service (Phases A/B/C implemented)
- [Feature Recommendations](docs/plans/feature-recommendations.md) — partially implemented
- [Regulatory Tracking Pipeline Plan](docs/plans/regtrack-pipeline-plan.md) — implemented as `menutracking` package
- [Weaviate Chunk Denormalization Plan](docs/plans/weaviate-chunk-denormalization-plan.md) — not yet started

**Not Started:**
- [Waiter Script Plan](docs/plans/waiter-script-plan.md)

**Archived:**
- [Python Extractor Service Plan](docs/plans/python-extractor-service-plan.md) — replaced by pure-Go OpenAI-compatible extractor

