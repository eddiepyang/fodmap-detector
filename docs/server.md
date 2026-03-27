# Go HTTP Server Architecture

The FODMAP detector includes a Go-based HTTP backend that provides APIs for business search, review retrieval, and an interactive chat agent. It features a dual-layer authentication system, conversation persistence via SQLite or PostgreSQL, and rate-limited LLM analysis jobs.

---

## Status: Operational ✅

The server is used by both the frontend web app and the `fodmap chat` CLI command. It reaches **68.7%** unit test coverage for core handlers.

---

## Project Structure (Server)

| Directory/File | Description |
|------|-------------|
| `server/server.go` | `Server` struct, routing, and lifecycle management |
| `server/auth_handler.go` | JWT-based registration, login, and token refresh |
| `server/conversation_handler.go` | CRUD for persisted chat histories and message sequences |
| `server/handlers.go` | Search and raw data access handlers |
| `server/middleware.go` | Auth enforcement, IP rate limiting, and concurrency control |
| `server/llm.go` | Gemini 2.0 Flash integration with concurrent workers and 15 RPM rate limiting |
| `auth/` | Package providing `Store` interface and database implementations |

---

## Key Features

### 1. Authentication & Security (`server/middleware.go`)

The server implements a robust middleware chain:
- **JWT Authentication**: Protects the `/api/v1` routes. Requires a valid `Bearer` token.
- **Combined Auth**: Fallback for CLI tools to use a static `API_KEY` for convenience while web users use JWT.
- **IP Rate Limiting**: Prevents abuse by limiting requests per IP address using `golang.org/x/time/rate`.
- **Concurrency Limiting**: caps the number of active long-running jobs to prevent resource exhaustion.

### 2. Conversation Persistence (`auth/`)

Chat sessions are persisted to a database (default: SQLite) to allow users to resume conversations across devices.
- **Schema**: Supports `users`, `conversations`, and `messages` with foreign key constraints.
- **History Recovery**: The server reconstructs the model's history from the database before sending new turns to Gemini, ensuring context consistency.

### 3. LLM Analysis Jobs (`server/llm.go`, `server/jobs.go`)

For high-volume review analysis, the server uses an async job system:
- Reviews are split into chunks of 10 and processed concurrently by 5 workers.
- Rate limits are enforced at the client level (15 RPM) to stay within Gemini's free tier.
- Job status can be polled via the `/results/{job_id}` endpoint.

---

## API Reference

### Authentication
- `POST /api/v1/auth/register` — Create a new user account.
- `POST /api/v1/auth/login` — Authenticate and receive JWT + Refresh tokens.
- `POST /api/v1/auth/refresh` — Issue a new JWT using a refresh token.

### Conversations
- `GET /api/v1/conversations` — List all conversations for the authenticated user.
- `GET /api/v1/conversations/{id}` — Get full history and metadata for a conversation.
- `DELETE /api/v1/conversations/{id}` — Delete a conversation and all its messages.

### Search & Data
- `GET /searchBusiness/{query}` — Semantic search for restaurants.
- `GET /searchReview/{query}` — Semantic search for specific review mentions.
- `GET /reviews?business_id=xxx` — Retrieve raw reviews for a restaurant.

### Analysis Jobs
- `POST /analyze?business_id=xxx` — Start an async analysis job (returns 202).
- `GET /results/{job_id}` — Poll for the status/result of an analysis job.

---

## Testing & Verification

The server package is tested using **mock dependency injection**. We use a `mockStore` that satisfies the `auth.Store` interface to verify handler logic without requiring a real database.

### Running Server Tests
```bash
go test -v ./server/...
```

### Coverage Goal
Current coverage: **68.7%**. The CI pipeline enforces a **70%** filtered coverage minimum across the project (excluding `cli/`).
