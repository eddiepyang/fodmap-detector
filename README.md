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
| LLM (parsing)  | Configured in the Python scraper service (defaults to Gemini)                                                                                    |
| Embeddings     | Ollama (`nomic-embed-text`)                                                                                                                      |

---

## Project Structure

The codebase is organized into several key packages (CLI, Chat, Server, Search, Auth). For a complete breakdown of the directory structure and what each package does, see the [Project Structure Guide](docs/guides/project-structure.md).

---

### Relational Database Backend
> [!IMPORTANT]
> **PostgreSQL is now the mandatory database backend.** SQLite support has been completely decommissioned.
> To run the server, `POSTGRES_DSN` is required (or starts PostgreSQL in Docker via `make start`). Schema migrations are managed by `golang-migrate` and embedded into the binary via `//go:embed`. Run `go run . db migrate-up` before starting the server (or use `./start.sh` which does this automatically).

### Role-Based Access Control (RBAC) & Admin Console
The server implements an RBAC model (User/Admin roles) and a comprehensive admin API for managing users, conversations, ingredient catalog, and analytics. For details on the roles, startup flags, and available endpoints, see the [RBAC & Admin Console Guide](docs/guides/admin-console.md).

---

## Documentation

We have split our documentation into separate guides and plans for easier reading.

### Guides & Playbooks
- [Quick Start & Setup](docs/guides/quick-start.md) - How to run the infrastructure and project locally.
- [Project Structure](docs/guides/project-structure.md) - Detailed breakdown of Go packages and sub-systems.
- [RBAC & Admin Console](docs/guides/admin-console.md) - Details on admin API endpoints and role management.
- [API Reference](docs/guides/api-reference.md) - HTTP endpoints, auth, and search.
- [Pipeline Architecture](docs/guides/pipeline-architecture.md) - High-level overview of the Go and Python data pipeline.
- [CLI Reference](docs/guides/cli-reference.md) - Commands for indexing, scraping, and chatting.
- [Pipeline CLI Guide](docs/guides/pipeline-cli.md) - Administering the discovery and scrape pipelines.
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
- [Restaurant Menu Discovery Plan](docs/plans/restaurant-menu-discovery-plan.md) — NYC OpenData → Gemini web-search discovery → River scrape pipeline (Astoria+LIC scope, Avro bronze/silver layer)
- [Feature Recommendations](docs/plans/feature-recommendations.md) — partially implemented
- [Regulatory Tracking Pipeline Plan](docs/plans/regtrack-pipeline-plan.md) — implemented as `menutracking` package
- [Weaviate Chunk Denormalization Plan](docs/plans/weaviate-chunk-denormalization-plan.md) — not yet started

**Not Started:**
- [Waiter Script Plan](docs/plans/waiter-script-plan.md)

**Archived:**
- [Python Extractor Service Plan](docs/plans/python-extractor-service-plan.md) — replaced by pure-Go OpenAI-compatible extractor

