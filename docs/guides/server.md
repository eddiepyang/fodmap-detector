# Go HTTP Server Architecture

The FODMAP detector includes a Go-based HTTP backend that provides APIs for business search, review retrieval, and an interactive chat agent. It features a dual-layer authentication system, conversation persistence via PostgreSQL, and rate-limited LLM analysis jobs.

---

## Status: Operational ✅

The server is used by both the frontend web app and the `fodmap chat` CLI command. It reaches **68.7%** unit test coverage for core handlers.

---

## Project Structure (Server)

| Directory/File | Description |
|------|-------------|
| `server/server.go` | `Server` struct, routing, and lifecycle management |
| `server/auth_handler.go` | JWT-based registration, login, token refresh, and user deletion |
| `server/admin_handler.go` | Admin console management endpoints (RBAC checks) |
| `server/chat_handler.go` | Real-time chat message streaming using Server-Sent Events (SSE) |
| `server/conversation_handler.go` | CRUD for persisted conversations |
| `server/conversation_export_handler.go` | JSON/Markdown conversation export handlers |
| `server/create_conversation.go` | Conversation creation and automatic review summarization |
| `server/handlers.go` | Search (business, review, FODMAP) and raw data access handlers |
| `server/profile_handler.go` | Dietary profile lookup and update endpoints |
| `server/middleware.go` | JWT validation, RBAC enforcement, rate limiting, and concurrency control |
| `auth/` | Package providing `ChatStore` and `AdminStore` database interfaces |

---

## Key Features

### 1. Authentication & Security (`server/middleware.go`)

The server implements a robust middleware chain:
- **JWT Authentication**: Protects the `/api/v1` routes. Requires a valid `Bearer` token.
- **RBAC Role Claims**: Maps users to roles ('user' or 'admin'). Admin handlers re-query the database to verify active status and role credentials on every request.
- **Combined Auth**: Fallback for CLI tools to use a static `API_KEY` for convenience while web users use JWT.
- **IP Rate Limiting**: Prevents abuse by limiting requests per IP address using `golang.org/x/time/rate`.
- **Concurrency Limiting**: caps the number of active long-running jobs to prevent resource exhaustion.

### 2. Conversation Persistence (`auth/`)

Chat sessions are persisted to a PostgreSQL database to allow users to resume conversations across devices.
- **Schema**: Supports `users`, `conversations`, and `messages` with foreign key constraints.
- **History Recovery**: The server reconstructs the model's history from the database before sending new turns to Gemini, ensuring context consistency.

### 3. SSE Chat Streaming (`server/chat_handler.go`)

For interactive chat sessions, the server supports real-time response streaming:
- Streamed responses are delivered via Server-Sent Events (SSE).
- Evaluates prompt length and screens for food-related topics prior to invoking Gemini.
- Uses Gemini's native API streaming capabilities under the hood.

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
- `GET /api/v1/search/businesses/{query...}` — Semantic search for restaurants.
- `GET /api/v1/search/reviews/{query...}` — Semantic search for specific review mentions.
- `GET /api/v1/search/fodmap/{ingredient...}` — FODMAP ingredient lookup.
- `GET /api/v1/reviews` — Retrieve raw reviews for a restaurant (query parameter: `business_id`).

### Profiles
- `GET /api/v1/profile` — Get the authenticated user's dietary profile.
- `POST /api/v1/profile` — Update the authenticated user's dietary profile.

### Chat & Streaming
- `POST /api/v1/conversations` — Create a conversation (automatically performs review summarization if reviews are present).
- `POST /api/v1/conversations/{id}/messages` — Send a message in a conversation and stream response (SSE).
- `GET /api/v1/conversations/{id}/export` — Export conversation transcript (format `json` or `markdown`).
- `POST /api/v1/chat/{query...}` — Direct/legacy streaming chat endpoint.

---

## Testing & Verification

The server package is tested using **mock dependency injection**. We use a `mockStore` that satisfies the `auth.AdminStore` interface to verify handler logic without requiring a real database.

### Running Server Tests
```bash
go test -v ./server/...
```

### Coverage Goal
Current coverage: **68.7%**. The CI pipeline enforces a **70%** filtered coverage minimum across the project (excluding `cli/`).
