# API Reference

### 3. Start the HTTP server

```sh
# With search enabled (Weaviate local)
go run . serve --postgres-dsn "postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable" --weaviate localhost:8090

# With search enabled (Pinecone Cloud)
go run . serve --postgres-dsn "postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable" --pinecone-api-key KEY --pinecone-index-host HOST --ollama-url http://localhost:11434 --ollama-model nomic-embed-text

# With search enabled (PostgreSQL/pgvector)
go run . serve --postgres-search --postgres-dsn "postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable" --ollama-url http://localhost:11434 --ollama-model nomic-embed-text

# Without search (search endpoint returns 503)
go run . serve --postgres-dsn "postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable"
```

Default port is `8081`.

#### Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/reviews` | — | List reviews for a business |
| `GET` | `/api/v1/search/businesses/{query...}` | — | Semantic business search |
| `GET` | `/api/v1/search/reviews/{query...}` | — | Semantic review search |
| `GET` | `/api/v1/search/fodmap/{ingredient...}` | — | FODMAP ingredient lookup |
| `POST` | `/api/v1/auth/register` | — | Register a new user account |
| `POST` | `/api/v1/auth/login` | — | Log in and receive access/refresh tokens |
| `POST` | `/api/v1/auth/refresh` | — | Exchange a refresh token for new tokens |
| `POST` | `/api/v1/auth/logout` | JWT | Log out (client-side token discard) |
| `DELETE` | `/api/v1/auth/user` | JWT | Delete the authenticated user's account |
| `GET` | `/api/v1/auth/me` | JWT | Get current user's profile info |
| `GET` | `/api/v1/conversations` | JWT | List conversations |
| `POST` | `/api/v1/conversations` | JWT | Create a new conversation |
| `GET` | `/api/v1/conversations/{id}` | JWT | Get a conversation |
| `DELETE` | `/api/v1/conversations/{id}` | JWT | Delete a conversation |
| `POST` | `/api/v1/conversations/{id}/messages` | JWT | Send a chat message (streaming) |
| `GET` | `/api/v1/conversations/{id}/export` | JWT | Export a conversation (JSON or Markdown) |
| `GET` | `/api/v1/profile` | JWT | Get dietary profile |
| `POST` | `/api/v1/profile` | JWT | Update dietary profile |
| `POST` | `/chat/{query...}` | JWT/API Key | Legacy chat endpoint (streaming) |
| `GET` | `/api/v1/admin/users` | JWT (Admin) | List active/suspended users |
| `GET` | `/api/v1/admin/users/{id}` | JWT (Admin) | Inspect user details & dietary profile |
| `PUT` | `/api/v1/admin/users/{id}/status` | JWT (Admin) | Toggle user status (active/suspended) |
| `DELETE` | `/api/v1/admin/users/{id}` | JWT (Admin) | Cascade delete user account & message history |
| `POST` | `/api/v1/admin/users/{id}/reset-password` | JWT (Admin) | Generate temporary password (bcrypt hash) |
| `GET` | `/api/v1/admin/conversations` | JWT (Admin) | List all conversations across the system |
| `GET` | `/api/v1/admin/conversations/{id}` | JWT (Admin) | Inspect a conversation's messages |
| `GET` | `/api/v1/admin/analytics/overview` | JWT (Admin) | Fetch total, active, suspended users, and signups |
| `GET` | `/api/v1/admin/analytics/activity` | JWT (Admin) | Fetch daily conversation activity stats |

**Conversation export** — the `GET /api/v1/conversations/{id}/export` endpoint supports a `format` query parameter:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `format` | `json` | Export format: `json` (machine-readable) or `markdown` (human-readable) |

```sh
# Export as JSON (default)
curl -H 'Authorization: Bearer <access_token>' localhost:8081/api/v1/conversations/42/export

# Export as Markdown
curl -H 'Authorization: Bearer <access_token>' "localhost:8081/api/v1/conversations/42/export?format=markdown"
```

**Rate limiting** — all endpoints are rate-limited. The server returns standard headers on every response:

| Header | Description |
|--------|-------------|
| `X-RateLimit-Limit` | Maximum requests allowed per window |
| `X-RateLimit-Remaining` | Requests remaining in the current window |
| `Retry-After` | Seconds until the limit resets (only present on `429 Too Many Requests` responses) |

**Common query parameters** for search endpoints:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `limit` | `10` | Max results to return |
| `category` | — | Filter by cuisine/category substring |
| `city` | — | Filter by city (exact match) |
| `state` | — | Filter by state (exact match) |
| `alpha` | `0` | Hybrid search weight: `0`=pure vector, `0.75`=balanced, `1`=pure vector |

#### Authentication

The server uses JWT-based authentication. Access tokens expire after **2 hours**; refresh tokens last **7 days**.

```sh
# Register
curl -X POST localhost:8081/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email": "user@example.com", "password": "mypassword"}'

# Login
curl -X POST localhost:8081/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email": "user@example.com", "password": "mypassword"}'
# → {"access_token": "...", "refresh_token": "...", "user": {...}}

# Use the access token for protected endpoints
curl -H 'Authorization: Bearer <access_token>' localhost:8081/api/v1/conversations

# Refresh tokens
curl -X POST localhost:8081/api/v1/auth/refresh \
  -H 'Content-Type: application/json' \
  -d '{"refresh_token": "..."}'

# Delete account (soft delete — the user is marked as deleted and cannot log in again)
curl -X DELETE -H 'Authorization: Bearer <access_token>' localhost:8081/api/v1/auth/user
# → {"message": "account deleted"}
```

> **Note:** Account deletion is a soft delete — the user's status is set to `"deleted"` and they are
> blocked from logging in or refreshing tokens. Existing access tokens remain valid until they expire
> (up to 2 hours). User data (conversations, messages) is retained for potential recovery.

#### Search endpoints

Find restaurants or review texts matching a natural-language description:

```sh
# Business search — returns top 10 businesses ranked by review relevance
curl "localhost:8081/api/v1/search/businesses/cozy%20Italian%20with%20great%20pasta"

# Filter by category, city, state
curl "localhost:8081/api/v1/search/businesses/best%20tacos?category=Mexican&city=Las%20Vegas&state=NV&limit=5"
```

##### Hybrid search (`?alpha=`)

All search endpoints support an optional `alpha` parameter that controls the balance between semantic vector search and BM25 keyword search:

| `alpha` value | Behaviour |
|--------------|-----------|
| Omitted / `0` | Pure semantic vector search (default, backward-compatible) |
| `0.0`–`1.0` | Hybrid: blend of BM25 and vector (higher = more vector weight) |
| `1.0` | Pure semantic vector search |

```sh
# Hybrid search: 75% vector + 25% BM25 keyword
curl "localhost:8081/api/v1/search/businesses/gluten%20free%20pizza?alpha=0.75"

# Heavily keyword-weighted (good for exact dish/ingredient names)
curl "localhost:8081/api/v1/search/reviews/pad%20thai?alpha=0.2"
```

On Weaviate, hybrid search uses the native `hybrid` operator with `relativeScoreFusion`. On Pinecone, BM25 re-ranking is applied in-process against the review `text` metadata field and blended with the dense vector score.

See [search.md](search.md) for full API reference and design decisions.


