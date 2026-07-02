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
| `GET` | `/api/v1/admin/ingredients` | JWT (Admin) | List ingredients with filters & pagination |
| `GET` | `/api/v1/admin/ingredients/stats` | JWT (Admin) | Aggregate counts by FODMAP level & group |
| `GET` | `/api/v1/admin/ingredients/search-test` | JWT (Admin) | Run semantic search test on ingredient catalog |
| `GET` | `/api/v1/admin/ingredients/{name}` | JWT (Admin) | Get a single ingredient by name |
| `POST` | `/api/v1/admin/ingredients` | JWT (Admin) | Create a new ingredient (no duplicates) |
| `PUT` | `/api/v1/admin/ingredients/{name}` | JWT (Admin) | Update an existing ingredient |
| `DELETE` | `/api/v1/admin/ingredients/{name}` | JWT (Admin) | Delete an ingredient from the catalog |
| `POST` | `/api/v1/admin/ingredients/reseed` | JWT (Admin) | Re-seed the catalog from the default database |
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

See [search.md](search.md) for design decisions.

---

#### Admin Endpoints

All admin endpoints require a JWT token belonging to a user with the `admin` role. The server validates the token and re-verifies the user's role and active status in the database on every call.

##### User & Conversation Administration

```sh
# List users with search, status filter, and pagination
curl -H 'Authorization: Bearer <admin_access_token>' \
  "localhost:8081/api/v1/admin/users?search=alex&status=active&page=1&limit=20"
# → {"users": [...], "total": 1, "page": 1, "limit": 20}

# Get detailed user info, message counts, and saved dietary profile
curl -H 'Authorization: Bearer <admin_access_token>' \
  localhost:8081/api/v1/admin/users/user_uuid_here

# Toggle user status (active / suspended)
curl -X PUT -H 'Authorization: Bearer <admin_access_token>' \
  -H 'Content-Type: application/json' \
  -d '{"status": "suspended"}' \
  localhost:8081/api/v1/admin/users/user_uuid_here/status
# → {"message": "status updated successfully"}

# Permanently delete a user account, cascading delete to all conversations and messages
curl -X DELETE -H 'Authorization: Bearer <admin_access_token>' \
  localhost:8081/api/v1/admin/users/user_uuid_here
# → {"message": "user deleted permanently"}

# Reset a user's password, returning a new random plaintext temporary password
curl -X POST -H 'Authorization: Bearer <admin_access_token>' \
  localhost:8081/api/v1/admin/users/user_uuid_here/reset-password
# → {"temporary_password": "..."}

# List all conversations in the system
curl -H 'Authorization: Bearer <admin_access_token>' \
  "localhost:8081/api/v1/admin/conversations?search=celiac&page=1&limit=20"

# Get conversation transcript messages
curl -H 'Authorization: Bearer <admin_access_token>' \
  localhost:8081/api/v1/admin/conversations/conv_uuid_here
```

##### Ingredient Catalog Administration

```sh
# List catalog ingredients with filters and pagination
curl -H 'Authorization: Bearer <admin_access_token>' \
  "localhost:8081/api/v1/admin/ingredients?search=garlic&level=high&group=fructan&page=1&limit=20"

# Get catalog stats (total counts, counts by FODMAP level and group)
curl -H 'Authorization: Bearer <admin_access_token>' \
  localhost:8081/api/v1/admin/ingredients/stats
# → {"total_count": 102, "level_counts": {"high": 45, ...}, "group_counts": {...}}

# Create a new ingredient entry in the catalog (auto-syncs to search index)
curl -X POST -H 'Authorization: Bearer <admin_access_token>' \
  -H 'Content-Type: application/json' \
  -d '{"ingredient": "shallot", "level": "high", "groups": ["fructan"], "notes": "High in fructans", "substitutions": ["chives", "green onion tops"]}' \
  localhost:8081/api/v1/admin/ingredients
# → returns 201 Created on success; returns 409 Conflict if duplicate

# Update an existing ingredient (name is immutable, other fields can be updated)
curl -X PUT -H 'Authorization: Bearer <admin_access_token>' \
  -H 'Content-Type: application/json' \
  -d '{"ingredient": "shallot", "level": "moderate", "groups": ["fructan"], "notes": "Moderate in small amounts", "substitutions": ["chives"]}' \
  localhost:8081/api/v1/admin/ingredients/shallot

# Delete an ingredient from the catalog
curl -X DELETE -H 'Authorization: Bearer <admin_access_token>' \
  localhost:8081/api/v1/admin/ingredients/shallot
# → {"message": "ingredient deleted"}

# Test-run a semantic search query against the ingredient catalog
curl -H 'Authorization: Bearer <admin_access_token>' \
  "localhost:8081/api/v1/admin/ingredients/search-test?q=onion"
# → returns matched ingredient name, details, and certainty score

# Re-seed the catalog database from the static Go dataset (FodmapDB) and rebuild index
curl -X POST -H 'Authorization: Bearer <admin_access_token>' \
  localhost:8081/api/v1/admin/ingredients/reseed
```

##### Analytics Overview & Activity

```sh
# Fetch system user analytics overview (total, active, suspended users, and recent signups)
curl -H 'Authorization: Bearer <admin_access_token>' \
  localhost:8081/api/v1/admin/analytics/overview
# → {"total_users": 15, "active_users": 14, "suspended_users": 1, ...}

# Fetch daily conversation activity stats (defaults to past 30 days, max 90)
curl -H 'Authorization: Bearer <admin_access_token>' \
  "localhost:8081/api/v1/admin/analytics/activity?days=30"
```

##### Scraper Pipeline Administration (Restaurants)

Admin-gated (same JWT + admin middleware). Registered only when the server
runs with the menusearch pipeline configured (`--enable-pipeline`). Backs the
admin console's **Scraper Pipeline** page.

```sh
# List restaurants with status filter, name search, and offset pagination
curl -H 'Authorization: Bearer <admin_access_token>' \
  "localhost:8081/api/v1/restaurants?status=failed_scrape&search=swick&limit=50&offset=0"
# → {"restaurants": [...], "total": 123, "limit": 50, "offset": 0}

# Pipeline stats rollup: status counts, tier mix, failure taxonomy, queue depth
curl -H 'Authorization: Bearer <admin_access_token>' \
  localhost:8081/api/v1/restaurants/stats
# → {
#     "total": 123, "total_items": 4567,
#     "status_counts": {"scraped": 80, "failed_scrape": 20, ...},
#     "tier_counts": {"jsonld": {"count": 12, "items": 340}, ...},
#     "failure_counts": {"extract menu": 10, "no menu items found": 5, ...},
#     "job_counts": {"menusearch.scrape_menu": {"retryable": 2, ...}, ...}
#   }
# failure_counts buckets last_error by its stage prefix with digit runs
# collapsed; job_counts limits finalized River jobs to the last 24h.

# Create a restaurant and enqueue discovery
curl -X POST -H 'Authorization: Bearer <admin_access_token>' \
  -H 'Content-Type: application/json' \
  -d '{"camis": "50012345", "dba": "Test Diner"}' \
  localhost:8081/api/v1/restaurants

# Get one restaurant by CAMIS
curl -H 'Authorization: Bearer <admin_access_token>' \
  localhost:8081/api/v1/restaurants/50012345

# Trigger jobs: discovery, scrape (requires menu_urls), or retry (auto-picks)
curl -X POST -H 'Authorization: Bearer <admin_access_token>' \
  localhost:8081/api/v1/restaurants/50012345/discover
curl -X POST -H 'Authorization: Bearer <admin_access_token>' \
  localhost:8081/api/v1/restaurants/50012345/scrape
curl -X POST -H 'Authorization: Bearer <admin_access_token>' \
  localhost:8081/api/v1/restaurants/50012345/retry
# → {"status": "queued"} (retry also returns "action": "discover"|"scrape")
# 409 if the job is already queued; 503 if the worker is not registered
```


