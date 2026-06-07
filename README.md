# FODMAP Detector

A Go CLI tool that processes Yelp dataset reviews to identify FODMAP (Fermentable Oligosaccharides, Disaccharides, Monosaccharides, and Polyols) content in food items. Designed to help people with digestive sensitivities by analyzing restaurant reviews for dish ingredients and flagging FODMAP groups.

---

## Purpose

1. Read Yelp review data from a compressed archive (`.tar.gz` of JSON lines)
2. Serialize reviews to Apache Avro (streaming) format
3. Provide an interactive semantic search and chat agent for FODMAP/allergen queries

---

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.26+ |
| CLI | [Cobra](https://github.com/spf13/cobra) |
| Streaming format | Apache Avro (OCF) via [hamba/avro](https://github.com/hamba/avro) |
| Input | TAR.GZ compressed JSON lines (Yelp dataset) |
| Concurrency | Go channels + goroutines |
| Vector search | [Weaviate](https://weaviate.io) (Local), [Pinecone](https://pinecone.io) (Cloud), or [PostgreSQL/pgvector](https://github.com/pgvector/pgvector) |
| Embeddings | Ollama |

---

## Project Structure

```
.
├── main.go                  # Server entry point
├── cli/
│   ├── root.go              # Root Cobra command
│   ├── event.go             # Avro subcommand (event write / event read)
│   ├── serve.go             # Serve subcommand (starts the HTTP server)
│   ├── index.go             # Index subcommand (populates Weaviate for search)
│   └── chat.go              # Chat subcommand (interactive FODMAP/allergen agent)
│
├── chat/
│   ├── chat.go              # Chat session logic, tool dispatch, system prompt rendering
│   ├── profile.go           # Dietary profile generation via Gemini
│   └── chat-instruction.txt # Embedded instruction template for the chat agent
│
├── server/
│   ├── server.go            # HTTP server setup and routes
│   ├── handlers.go          # Search & FODMAP HTTP handlers
│   ├── auth_handler.go      # Auth endpoints (register, login, refresh, delete)
│   ├── chat_handler.go      # Chat streaming handler (SSE)
│   ├── conversation_handler.go       # Conversation CRUD endpoints
│   ├── conversation_export_handler.go # Conversation export (JSON/Markdown)
│   ├── create_conversation.go        # Conversation creation + review summary
│   ├── direct_fodmap_client.go       # Direct FODMAP lookup client for chat
│   ├── profile_handler.go             # Dietary profile endpoints
│   ├── middleware.go         # JWT auth, rate limiting, CORS middleware
│   └── mock_store.go         # In-memory test store
│
├── data/
│   ├── data.go              # Archive reading, Parquet write/read
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
│   ├── store.go             # Unified Store interface
│   ├── sqlite_store.go      # SQLite implementation
│   ├── postgres_store.go    # PostgreSQL implementation
│   ├── conversation.go      # Conversation & Message models
│   ├── jwt.go               # Token generation/validation
│   └── user.go              # User model
│
├── docs/
│   ├── guides/                  # Playbooks, API/CLI references, system design
│   └── plans/                   # Feature implementation plans and roadmaps
│
└── docker-compose.yaml      # Vector database configuration (Weaviate)
```

---

## Documentation

We have split our documentation into separate guides and plans for easier reading.

### Guides & Playbooks
- [Quick Start & Setup](docs/guides/quick-start.md) - How to run the infrastructure and project locally.
- [API Reference](docs/guides/api-reference.md) - HTTP endpoints, auth, and search.
- [CLI Reference](docs/guides/cli-reference.md) - Commands for indexing, scraping, and chatting.
- [Data Model & Pipeline](docs/guides/data-model.md) - Core data structures and inputs.
- [Testing](docs/guides/testing.md) - How to run tests and coverage targets.
- [Search Design](docs/guides/search.md) - Search service design decisions.
- [Chat Design](docs/guides/chat.md) - Chat agent design decisions.
- [Server Design](docs/guides/server.md) - Server design and API docs.
- [LLM Serving](docs/guides/llm-serving.md) - LLM serving architectures.

### Plans & Roadmaps
- [Frontend Implementation Plan](docs/plans/FRONTEND_IMPLEMENTATION_PLAN.md)
- [Scraper Pipeline Plan](docs/plans/scraper-pipeline-plan.md)
- [Dietary Profile Plan](docs/plans/dietary-profile-plan.md)
- [Python Extractor Service Plan](docs/plans/python-extractor-service-plan.md)
- [Indexing Improvements](docs/plans/indexing-improvements.md)
- [Feature Recommendations](docs/plans/feature-recommendations.md)
- [Deleted User Plan](docs/plans/handle-deleted-user-plan.md)
- [Waiter Script Plan](docs/plans/waiter-script-plan.md)
