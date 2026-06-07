# Go HTTP Server Architecture

The FODMAP detector includes a Go-based HTTP backend that provides APIs for business search, review retrieval, FODMAP ingredient lookup, and an interactive chat agent. It features JWT-based authentication, conversation persistence via SQLite or PostgreSQL, rate-limited streaming chat, and a menu-tracking pipeline.

---

## Status: Operational ✅

The server is used by both the frontend web app and the `fodmap chat` CLI command.

---

## Project Structure (Server)

| Directory/File | Description |
|------|-------------|
| `server/server.go` | `Server` struct, routing, and lifecycle management |
| `server/auth_handler.go` | JWT-based registration, login, and token refresh |
| `server/chat_handler.go` | SSE streaming chat handler, model factory |
| `server/conversation_handler.go` | CRUD for persisted chat histories and message sequences |
| `server/conversation_export_handler.go` | Conversation export (JSON/Markdown) |
| `server/create_conversation.go` | Conversation creation + review summary |
| `server/handlers.go` | Search and FODMAP lookup handlers |
| `server/profile_handler.go` | Dietary profile endpoints |
| `server/middleware.go` | Auth enforcement, IP rate limiting, and concurrency control |
| `server/llm.go` | Gemini integration with concurrent workers |

---

## Key Features

### 1. Authentication & Security (`server/middleware.go`)

The server implements a layered middleware chain:
- **JWT Authentication** (`jwtAuth`): Protects `/api/v1` routes requiring a logged-in user.
- **Combined Auth** (`combinedAuth`): Fallback for chat endpoints — accepts either a JWT `Bearer` token or a static `API_KEY`.
- **IP Rate Limiting**: Per-IP token-bucket rate limiter (default: 2 req/s burst 5) using `golang.org/x/time/rate`. Uses `X-Forwarded-For` header when present.
- **Concurrency Limiting**: Caps the number of concurrent long-running chat requests (default: 10). Returns `503` when all slots are full.

Middleware is chained so that auth runs outermost — unauthenticated requests don't consume rate-limit tokens.

### 2. Conversation Persistence (`auth/`)

Chat sessions are persisted to a database (default: SQLite; PostgreSQL also supported) to allow users to resume conversations across devices.
- **Schema**: Supports `users`, `user_profiles`, `conversations`, and `messages` with foreign key constraints.
- **History Recovery**: The server reconstructs the model's history from the database before sending new turns to Gemini, ensuring context consistency.

### 3. Chat Endpoints (`server/chat_handler.go`)

Streaming chat is exposed via two paths (both require auth):
- `POST /api/v1/chat/{query...}` — canonical JWT-authenticated endpoint
- `POST /chat/{query...}` — legacy endpoint accepting `Bearer` token or `X-API-Key` header

Both support SSE streaming. The `create_conversation.go` handler also supports `POST /api/v1/conversations` with an initial message that streams a response.

---

## API Reference

See [api-reference.md](api-reference.md) for the full endpoint catalog.

### Authentication
- `POST /api/v1/auth/register` — Create a new user account.
- `POST /api/v1/auth/login` — Authenticate and receive JWT + Refresh tokens.
- `POST /api/v1/auth/refresh` — Issue a new JWT using a refresh token.
- `POST /api/v1/auth/logout` — Log out (client-side token discard).
- `DELETE /api/v1/auth/user` — Delete the authenticated user's account (soft delete).

### Search & Data
- `GET /api/v1/search/businesses/{query...}` — Semantic search for restaurants.
- `GET /api/v1/search/reviews/{query...}` — Semantic search for review mentions.
- `GET /api/v1/search/fodmap/{ingredient...}` — FODMAP ingredient lookup.
- `GET /api/v1/reviews?business_id=xxx` — Retrieve raw reviews for a business.

### Conversations & Chat
- `GET /api/v1/conversations` — List conversations for the authenticated user.
- `POST /api/v1/conversations` — Create a new conversation.
- `GET /api/v1/conversations/{id}` — Get a conversation with full history.
- `DELETE /api/v1/conversations/{id}` — Delete a conversation and all its messages.
- `POST /api/v1/conversations/{id}/messages` — Send a chat message (SSE streaming).
- `GET /api/v1/conversations/{id}/export?format=json|markdown` — Export a conversation.

### Profile
- `GET /api/v1/profile` — Get dietary profile.
- `POST /api/v1/profile` — Update dietary profile.

### Menu Tracking (requires auth)
- `GET /menutracking/sources` — List menu sources.
- `GET /menutracking/jobs` — List scraping jobs.
- `POST /menutracking/reload` — Reload sources from config.

---

## Testing & Verification

The server package is tested using dependency injection. Mock/stub types satisfy interfaces like `auth.Store` and `search.Searcher` to verify handler logic without requiring real infrastructure.

### Running Server Tests
```bash
go test -v ./server/...
```

### Coverage Goal
CI enforces a **70%** filtered coverage minimum across the project (excluding `cli/`). Run locally:
```bash
make check
```