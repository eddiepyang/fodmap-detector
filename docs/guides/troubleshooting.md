# Troubleshooting Guide

This guide covers common issues, diagnostic procedures, and migration instructions for the FODMAP Detector application.

---

## 1. Stale Weaviate Schema Migrations

### The Problem
During development, if a property type changes in the Go data structures (for example, the `substitutions` field on `FodmapIngredient` changing from a single `text` string to a `text[]` string array), Weaviate will not dynamically update the schema of an existing class. On startup, the server will fail to load or batch upsert entries and write warning/error logs such as:

```
invalid text property 'substitutions' on class 'FodmapIngredient': not a string, but []interface {}
```

Similarly, if search is returning empty results (`{"businesses":[]}`) for every query (even general keywords like `"beer"` or `"pizza"`), the cross-references between reviews and chunks may be corrupted or using an obsolete format.

> [!IMPORTANT]
> **This schema migration must be performed manually on all running instances of the application** (local development machines, staging environments, production deployments, etc.) when deploying updates that modify the database structures.

### Solution: Dropping Obsolete Schema Classes
To resolve type mismatches, you must drop the stale Weaviate classes and let the Go backend recreate them cleanly on the next startup:

```bash
# Delete the FodmapIngredient schema (recreated automatically on serve startup)
curl -X DELETE http://localhost:8090/v1/schema/FodmapIngredient

# Delete the YelpReview schemas (requires running the index command again)
curl -X DELETE http://localhost:8090/v1/schema/YelpReviewChunk
curl -X DELETE http://localhost:8090/v1/schema/YelpReview
```

---

## 2. Querying Weaviate Directly for Diagnostics

If you need to verify that records are correctly indexed and reference linkage is intact, you can query Weaviate directly using standard curl requests.

### Check Existing Schema Properties
Inspect properties and types currently registered in Weaviate:
```bash
curl -s http://localhost:8090/v1/schema
```

### Fetch Objects via REST API
Fetch sample records directly from specific classes:
```bash
# Fetch a YelpReview
curl -s "http://localhost:8090/v1/objects?class=YelpReview&limit=1"

# Fetch a YelpReviewChunk
curl -s "http://localhost:8090/v1/objects?class=YelpReviewChunk&limit=1"
```

### Validate Reference Linkage via GraphQL
Execute a GraphQL query to ensure that `YelpReviewChunk` is successfully linked back to its parent `YelpReview` record:
```bash
curl -s -X POST \
  -H "Content-Type: application/json" \
  -d '{"query": "{ Get { YelpReviewChunk (limit: 2) { chunkText hasParent { ... on YelpReview { reviewId businessId businessName } } } } }"}' \
  http://localhost:8090/v1/graphql
```

#### Successful Response Format
For single-target references, Weaviate flat-maps the properties inside the `hasParent` array:
```json
{
  "data": {
    "Get": {
      "YelpReviewChunk": [
        {
          "chunkText": "Good selection of your Thai favorites...",
          "hasParent": [
            {
              "businessId": "tmYa9OC8NE4ov2BoLyL2WQ",
              "businessName": "Thai Island",
              "reviewId": "z0acnaJ9GKC7-cElSspbNg"
            }
          ]
        }
      ]
    }
  }
}
```

---

## 3. Port Conflicts (`bind: address already in use`)

### The Problem
If the backend or frontend fails to start with errors such as:
```
server error: listen tcp :8081: bind: address already in use
```
An existing instance of the server is already running in the background.

### Solution
1. Find the PID of the process using the port:
   ```bash
   # For the backend (8081)
   lsof -i :8081

   # For the frontend (5173)
   lsof -i :5173
   ```
2. Terminate the process:
   ```bash
   kill <PID>
   # Or forcefully if it resists
   kill -9 <PID>
   ```
3. Restart using `make start` or `make run`.

---

## 4. Chat Streaming: Corrupted Thought Signature (400)

### The Problem
During a chat stream, the application may fail with an "unknown streaming error", and the server logs will show:

```
model generation error: stream error: Error 400, Message: Corrupted thought signature., Status: INVALID_ARGUMENT
```

### The Cause
Gemini models (particularly reasoning models like `gemini-3-flash-preview`) output a binary **thought signature** block when performing internal thinking or function calling. When passing this history back to Gemini in subsequent turns, the exact binary tokens must be provided. 

Previously, the backend converted the raw binary bytes of the signature directly to a Go `string` (`string(part.ThoughtSignature)`). Since Go strings expect valid UTF-8 sequences, any arbitrary binary bytes that did not conform to UTF-8 were corrupted/replaced. When the backend cast this string back to a byte slice (`[]byte`) to send to Gemini, the model rejected it as a corrupted thought signature.

### The Solution
We updated the `chat` package to safely encode the `ThoughtSignature` into a **base64 string** before serialization, and decode it back to the exact original binary bytes when sending the conversation history to the API:

- **Serialization**: `base64.StdEncoding.EncodeToString(part.ThoughtSignature)`
- **Deserialization**: `base64.StdEncoding.DecodeString(fc.ThoughtSignature)`

If you are developing custom clients or wrappers that manage Gemini's reasoning history, ensure you serialize binary thought signatures as base64 strings rather than converting them directly to raw UTF-8 strings.

---

## 5. Admin & User Account Diagnostics (PostgreSQL)

During testing, you may encounter issues registering admin/user accounts (e.g. duplicate key errors) or need to manually check user roles.

### Diagnosing Duplicate Registrations
If registration fails with the following error:
```
failed to create user: ERROR: duplicate key value violates unique constraint "users_email_key" (SQLSTATE 23505)
```
The user is already registered in the PostgreSQL database.

### Direct Database Queries via Docker
Since PostgreSQL runs inside a Docker container (`fodmap-detector-postgres-1`), you can run `psql` queries directly from your shell:

#### 1. Search for a Specific User Record
```bash
docker exec -it fodmap-detector-postgres-1 psql -U fodmap -d fodmap -c "SELECT id, email, role, status, created_at FROM users WHERE email = 'admin@example.com';"
```

#### 2. List All Registered Users
```bash
docker exec -it fodmap-detector-postgres-1 psql -U fodmap -d fodmap -c "SELECT id, email, role, status FROM users;"
```

#### 3. Delete/Reset an Account (for Fresh Registration)
To delete a user account so you can register it fresh through the UI:
```bash
docker exec -it fodmap-detector-postgres-1 psql -U fodmap -d fodmap -c "DELETE FROM users WHERE email = 'admin@example.com';"
```

#### 4. Manually Promote a User to Admin
If you need to manually grant a registered user admin privileges:
```bash
docker exec -it fodmap-detector-postgres-1 psql -U fodmap -d fodmap -c "UPDATE users SET role = 'admin' WHERE email = 'user@example.com';"
```

---

## 6. Postgres Migration Issues

Domain table DDL is managed by `golang-migrate` and applied via `go run . db migrate-up`. The migration state is tracked in the `schema_migrations` table.

### Check current migration version
```bash
go run . db migrate-version
```

### Migrations fail on existing database
If `db migrate-up` fails on a database that already has tables (e.g. from before the migration system was introduced), the baseline migration uses `IF NOT EXISTS` and should be safe. If you see errors about duplicate objects, you can force-mark the baseline as applied:
```bash
go run . db migrate-force 1
```
Then run `db migrate-up` again to apply any remaining migrations.

### Reset migrations (destructive)
To wipe the migration state and start fresh (only for development):
```bash
go run . db migrate-down
```
This drops all domain tables. You can then run `db migrate-up` to recreate them.

### River's own tables
River's schema tables (`river_job`, `river_leader`, `river_queue`, `river_client`) are managed separately by `river migrate-up` and are **not** included in `golang-migrate` migrations. Do not attempt to manage them with `db migrate-up/down`.


