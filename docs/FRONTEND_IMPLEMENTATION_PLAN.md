# Frontend Implementation Plan

This document outlines a plan for evolving the fodmap-detector CLI/API into a web application. The backend already has a working chat endpoint with Gemini streaming, bearer token auth, per-IP rate limiting, and concurrency control. The plan builds incrementally on what exists.

## High-Level Plan

The implementation is divided into five phases, executed incrementally so the CLI and existing API remain functional throughout:

1. **SSE Streaming & CORS** — Add streaming responses and cross-origin support to the existing chat endpoint.
2. **Authentication** — Add JWT-based user auth alongside the existing bearer token.
3. **Conversation Persistence** — Store multi-turn chat history in SQLite.
4. **Frontend** — Build a React SPA for login and chat.
5. **Data Layer & Deployment** — Add Pinecone support and deploy to Cloud Run.

---

## Phase 1: SSE Streaming & CORS

### SSE Streaming (Leveraging Existing Architecture)

The backend already streams internally — `Session.SendWithToolCalls()` in `chat/chat.go` accepts an `onText func(string)` callback that fires as Gemini chunks arrive. This maps directly to Server-Sent Events.

**How it works:**
1. The frontend sends a message via `POST /api/v1/conversations/{id}/messages`.
2. The backend sets `Content-Type: text/event-stream` and keeps the connection open.
3. As Gemini generates chunks, the `onText` callback writes SSE frames:
   ```
   data: {"type":"chunk","text":"This restaurant"}

   data: {"type":"chunk","text":" has great options"}

   data: {"type":"done","tool_calls":["lookup_fodmap(garlic)"]}
   ```
4. The frontend reads the stream via the Fetch API's `ReadableStream` (not `EventSource`, since this is a POST response).

**Why SSE over WebSockets:** The app only needs server-to-client streaming for LLM responses. Client-to-server messages use standard POST requests. WebSockets would add protocol upgrades, a connection hub, sticky sessions, and custom reconnection logic — none of which are needed.

**Backend changes:**
* New SSE-capable chat handler alongside the existing JSON handler (or a query parameter `?stream=true` to opt in)
* Use `http.Flusher` to push chunks as they arrive
* SSE event types: `chunk` (partial text), `tool` (tool call notification), `done` (final message), `error`

### CORS Middleware

Add CORS middleware to `server/middleware.go` to allow requests from the frontend origin.

* Configurable allowed origins via environment variable (e.g., `CORS_ORIGINS=http://localhost:3000,https://app.example.com`)
* Allow `Authorization`, `Content-Type` headers
* Handle preflight `OPTIONS` requests

### Fix Self-Referential HTTP Client

Currently `chat_handler.go:163` creates an HTTP client that calls the server itself for FODMAP lookups:
```go
fodmapClient := chat.NewHTTPFodmapServerClient("http://" + r.Host)
```

Create a `DirectFodmapClient` adapter in the `server` package that implements `chat.FodmapServerClient` by calling `s.searcher.SearchFodmap()` directly, eliminating the unnecessary HTTP round-trip.

---

## Phase 2: Authentication

### JWT-Based Auth

A JWT approach is recommended for its stateless nature and suitability for SPAs.

**Workflow:**
1. User registers or logs in via `POST /api/v1/auth/register` or `POST /api/v1/auth/login`.
2. Backend validates credentials, returns a JWT (short-lived access token + longer-lived refresh token).
3. Frontend stores the JWT in an `HttpOnly` cookie and includes it in the `Authorization` header.
4. New `jwtAuth` middleware in `server/middleware.go` validates the token on protected endpoints.

**New `auth/` package:**
* `User` model (ID, email, hashed password, created_at)
* `UserStore` interface with SQLite and PostgreSQL implementations
* JWT token generation and validation
* Password hashing with `bcrypt`

The new `auth/` package sits alongside existing packages (`chat/`, `server/`, `search/`, `data/`, `cli/`) — no restructuring needed. The existing package layout is already well-separated by domain.

**User data store — interface-driven, swappable backends:**

All database access goes through a `UserStore` interface so the backing store can be swapped at startup:

```go
type UserStore interface {
    Create(ctx context.Context, user *User) error
    GetByEmail(ctx context.Context, email string) (*User, error)
    GetByID(ctx context.Context, id string) (*User, error)
}
```

Two implementations:
* **`SQLiteUserStore`** — via `modernc.org/sqlite` (pure Go, no CGO). Zero setup, ideal for local dev and low-volume production.
* **`PostgresUserStore`** — via `pgx`. Better for concurrent writes and multi-instance deployments.

Selected at startup based on config:
```go
if cfg.DatabaseURL != "" {  // e.g. postgres://...
    store = auth.NewPostgresUserStore(cfg.DatabaseURL)
} else {
    store = auth.NewSQLiteUserStore(cfg.SQLitePath)
}
```

**SQL compatibility note:** Write all SQL to be compatible with both SQLite and PostgreSQL. Use `TEXT` (not `VARCHAR`), avoid `AUTOINCREMENT`, use `TIMESTAMP` with `DEFAULT CURRENT_TIMESTAMP`. This keeps the migration path open.

```sql
CREATE TABLE users (
    id         TEXT PRIMARY KEY,
    email      TEXT UNIQUE NOT NULL,
    password   TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

**Coexistence with bearer token auth:**
* The existing `bearerAuth` middleware continues to work for API clients and the CLI.
* New `jwtAuth` middleware is used for browser-based requests.
* A combined auth middleware checks for JWT first, falls back to bearer token.

### Account-Level Rate Limiting

Supplement the existing per-IP rate limiting with per-user limits:
* Authenticated requests are rate-limited by user ID (not IP)
* Track daily LLM usage per user to prevent cost abuse
* Add CAPTCHA (e.g., Turnstile) on registration to prevent bot signups

---

## Phase 3: Conversation Persistence

This is the most significant architectural change. The current chat endpoint is stateless — it creates a fresh `Session` per request with no stored history.

### Storage Schema

Same dual-store pattern as `auth/` — a `ConversationStore` interface with SQLite and PostgreSQL implementations, swappable at startup. SQL is written to be compatible with both databases.

```sql
CREATE TABLE conversations (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id),
    business_id TEXT NOT NULL,
    title       TEXT NOT NULL,
    created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE messages (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    role            TEXT NOT NULL,          -- 'user', 'model', 'tool_call', 'tool_response'
    content         TEXT NOT NULL,          -- message text or JSON for tool calls/responses
    sequence        INTEGER NOT NULL,       -- ordering within conversation
    created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_messages_conversation ON messages(conversation_id, sequence);
```

```go
type ConversationStore interface {
    CreateConversation(ctx context.Context, conv *Conversation) error
    ListByUser(ctx context.Context, userID string) ([]Conversation, error)
    GetMessages(ctx context.Context, conversationID string) ([]Message, error)
    AddMessage(ctx context.Context, msg *Message) error
    Delete(ctx context.Context, conversationID string) error
}
```

### History Reconstruction

To continue a conversation, the backend must rebuild `Session.History` (`[]*genai.Content`) from stored messages:
* `user` messages become `genai.Content{Role: "user", Parts: [Text]}`
* `model` messages become `genai.Content{Role: "model", Parts: [Text]}`
* `tool_call` messages store the function name and arguments as JSON
* `tool_response` messages store the tool result as JSON

### Token Budgeting

Gemini has context window limits. For long conversations:
* Track approximate token count per message (rough estimate: `len(text) / 4`)
* When history exceeds 80% of the model's context window, truncate oldest messages (keeping the system prompt and most recent N turns)
* Store all messages in the database regardless — truncation only affects what's sent to Gemini

### Session-to-Business Binding

Each conversation is scoped to one business (the system prompt embeds business info and reviews). The `conversations` table stores `business_id`, and the system prompt is regenerated from that business when resuming a conversation.

### API Endpoints

* `POST /api/v1/conversations` — Create a new conversation (accepts query, category, city, state; finds the top business and returns conversation ID + business info)
* `GET /api/v1/conversations` — List conversations for the authenticated user
* `GET /api/v1/conversations/{id}` — Get conversation details and message history
* `POST /api/v1/conversations/{id}/messages` — Send a message and stream the response (SSE)
* `DELETE /api/v1/conversations/{id}` — Delete a conversation

---

## Phase 4: Frontend (Separate Repository)

The frontend lives in its own repository (e.g., `fodmap-detector-web`) — different language (TypeScript), different build toolchain (Node/Vite), different deploy artifact (static files). Keeping it separate avoids polluting the Go repo with `node_modules`, `package.json`, and JS tooling config.

### Technology Stack

* **Language:** TypeScript
* **Framework:** React with Vite — this is a chat SPA with no SEO needs or public content to pre-render, so SSR (Next.js) adds complexity without payoff. Vite provides fast builds and HMR.
* **UI Toolkit:** Shadcn/ui — composable, accessible components built on Radix primitives. Tailwind CSS for styling.
* **State Management:** TanStack Query for server state (conversations, messages). Zustand for minimal client state (active conversation ID, auth state).

### Repository Structure

```
fodmap-detector-web/          # separate repo
  package.json
  tsconfig.json
  vite.config.ts
  src/
    routes/
      login.tsx               # Login/register page
      chat.tsx                # Main chat interface
    components/
      ChatWindow.tsx          # Message list + input
      ConversationList.tsx    # Sidebar with conversation history
      MessageBubble.tsx       # Single message display
      AuthForm.tsx            # Login/register form
    api/
      client.ts               # Base HTTP client with auth headers
      auth.ts                 # Login, register, refresh token
      conversations.ts        # CRUD + message streaming
    hooks/
      useSSEStream.ts         # Hook for reading SSE response streams
      useAuth.ts              # Auth state and token management
    types/
      api.ts                  # Shared API request/response types
    App.tsx
    main.tsx
```

Refactor into a more layered architecture later if the app grows beyond 3-4 pages.

### Local Development

* Backend runs on `localhost:8081` (existing `serve` command)
* Frontend runs on `localhost:5173` (Vite dev server)
* Vite proxy config forwards `/api/` requests to the backend during development, avoiding CORS issues locally:
  ```typescript
  // vite.config.ts
  export default defineConfig({
    server: {
      proxy: { '/api': 'http://localhost:8081' },
    },
  });
  ```

### API Type Safety

Define TypeScript types that mirror the backend's JSON contracts in `src/types/api.ts`. If the API surface grows, consider generating types from an OpenAPI spec exported by the Go backend.

### SSE Integration

```typescript
// Simplified useSSEStream hook
async function streamMessage(conversationId: string, message: string) {
  const res = await fetch(`/api/v1/conversations/${conversationId}/messages`, {
    method: 'POST',
    headers: { 'Authorization': `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ message }),
  });

  const reader = res.body!.getReader();
  const decoder = new TextDecoder();

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    const chunk = decoder.decode(value);
    // Parse SSE frames: "data: {...}\n\n"
    // Update message state with each chunk
  }
}
```

---

## Phase 5: Data Layer & Deployment

### Vector Database Agnosticism

The backend's `Searcher` interface in `server/server.go` already provides the right abstraction:

```go
type Searcher interface {
    GetBusinesses(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchResult, error)
    GetReviews(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchReviews, error)
    SearchFodmap(ctx context.Context, ingredient string) (search.FodmapResult, float64, error)
}
```

To support Pinecone alongside Weaviate, create a `PineconeSearcher` that implements this same interface. No new `VectorStore` abstraction is needed.

**Implementation:**
1. **`PineconeSearcher`** — Implements `Searcher` by wrapping the Pinecone Go client + an embedding service (Gemini's embedding API). Text queries are vectorized first, then searched against Pinecone indexes.
2. **`EmbeddingService` interface** — Abstracts vector embedding generation. The `PineconeSearcher` depends on this; the existing Weaviate client does not (Weaviate handles vectorization internally).
3. **Dependency injection** — At startup, the server creates either a Weaviate `search.Client` or a `PineconeSearcher` based on `VECTOR_DB_PROVIDER` env var. Both satisfy `Searcher`.

**Local development:** Use Weaviate via Docker (already in `docker-compose.yaml`).
**Production:** Use Pinecone Serverless free tier.

### Deployment (Cloud Run)

**Estimated cost: ~$0-5/month** for low volume.

* **Compute:** Go backend on Cloud Run. Frontend static build served via Cloud Run (lightweight nginx container) or Firebase Hosting / Cloud CDN for better caching. Both scale to zero.
* **Database persistence (critical concern):** Cloud Run instances are ephemeral — they can be terminated at any time. Two paths depending on which store backend is chosen:
  * **SQLite path:**
    * **Cloud Run Volume Mounts** (recommended) — Mount a persistent disk. GA since 2024, simplest option. Limits horizontal scaling since the disk attaches to one container, but fine for low volume.
    * **Litestream + GCS** — Continuously replicate SQLite WAL to a Cloud Storage bucket. Supports scale-to-zero but adds operational complexity and a small risk of data loss on crash.
  * **PostgreSQL path:**
    * **Cloud SQL (PostgreSQL)** — Managed database, free tier available. Eliminates all persistence concerns and supports multi-instance scaling. Slightly higher cost (~$7/mo after free tier) but the most operationally simple production option.
  * Because both `UserStore` and `ConversationStore` are interface-driven, you can start with SQLite locally and switch to Cloud SQL PostgreSQL in production by setting `DATABASE_URL`.
* **Vector database:** Pinecone Serverless free tier.
* **HTTPS/Load balancing:** Included free with Cloud Run.

### Alternative: GKE

**Estimated cost: ~$33-40/month.** Only if Kubernetes is strictly required.
* Free zonal cluster + `e2-medium` Spot VM (~$10/mo) + HTTP(S) Load Balancer (~$18/mo) + storage (~$5/mo).

---

## CLI Backward Compatibility

The existing `chat` and `serve` CLI commands must continue working throughout all phases:

* The `serve` command gains new flags (`--cors-origins`, `--jwt-secret`) but existing flags remain unchanged.
* The `chat` CLI command continues to work directly with Gemini — it doesn't go through the HTTP API for the chat session itself, so auth changes don't affect it.
* The existing bearer token auth remains supported alongside JWT. API clients (including the CLI's HTTP calls for business/review search) continue using bearer tokens.

---

## Phased TODO List

- [ ] **Phase 1 — SSE & CORS:**
    - [ ] Add CORS middleware to `server/middleware.go`
    - [ ] Create `DirectFodmapClient` adapter (replace self-referential HTTP call)
    - [ ] Add SSE streaming chat handler using existing `onText` callback
    - [ ] Define SSE event format (`chunk`, `tool`, `done`, `error`)
- [ ] **Phase 2 — Authentication:**
    - [ ] Create `auth/` package (User model, `UserStore` interface)
    - [ ] Implement `SQLiteUserStore` (`modernc.org/sqlite`) and `PostgresUserStore` (`pgx`)
    - [ ] Implement JWT generation/validation with refresh tokens
    - [ ] Add `jwtAuth` middleware, combined with existing `bearerAuth`
    - [ ] Add `POST /api/v1/auth/register` and `POST /api/v1/auth/login` endpoints
    - [ ] Add per-user rate limiting for LLM endpoints
- [ ] **Phase 3 — Conversation Persistence:**
    - [ ] Design database schema (conversations + messages tables, compatible with both SQLite and PostgreSQL)
    - [ ] Implement `ConversationStore` interface with SQLite and PostgreSQL impls
    - [ ] Build history reconstruction (`messages` rows -> `[]*genai.Content`)
    - [ ] Add token budgeting / history truncation
    - [ ] Implement conversation CRUD endpoints
    - [ ] Wire SSE streaming into `POST /api/v1/conversations/{id}/messages`
- [ ] **Phase 4 — Frontend (separate repo: `fodmap-detector-web`):**
    - [ ] Create new repo, scaffold React + Vite + TypeScript project with Shadcn/ui
    - [ ] Configure Vite proxy for local backend development
    - [ ] Define API types in `src/types/api.ts`
    - [ ] Build auth pages (login/register)
    - [ ] Build chat UI (conversation list, message window, SSE streaming)
    - [ ] Implement `useSSEStream` hook for real-time message display
- [ ] **Phase 5 — Data Layer & Deployment:**
    - [ ] Implement `PineconeSearcher` satisfying existing `Searcher` interface
    - [ ] Add `EmbeddingService` for Pinecone vectorization
    - [ ] Add env-based provider selection (`VECTOR_DB_PROVIDER`)
    - [ ] Write multi-stage Dockerfile for Go backend
    - [ ] Write Dockerfile (or Firebase Hosting config) for frontend static build
    - [ ] Set up GitHub Actions CI/CD for both repos
    - [ ] Configure Cloud Run with database persistence strategy
