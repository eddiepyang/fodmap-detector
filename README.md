# FODMAP Detector

A full-stack application (Go HTTP API backend and React SPA frontend) featuring an AI-powered chat assistant that analyzes restaurant menus and Yelp reviews to identify FODMAP (Fermentable Oligosaccharides, Disaccharides, Monosaccharides, and Polyols) content and food allergens. Designed to help individuals with digestive sensitivities make safe dining choices using personalized dietary profiles, hybrid vector-grounded search, and a comprehensive administrative RBAC console.

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

### Role-Based Access Control (RBAC) & Admin Console
The server implements an RBAC model (User/Admin roles) and a comprehensive admin API for managing users, conversations, ingredient catalog, and analytics. For details on the roles, startup flags, and available endpoints, see the [RBAC & Admin Console Guide](docs/guides/admin-console.md).

---

## Documentation

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


