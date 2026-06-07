# Restaurant Search Service

Semantic search over Yelp restaurant reviews using Weaviate and locally-run transformer embeddings.
Given a natural-language description, the endpoint returns restaurant (business) IDs ranked by how
well their reviews match the query — optionally filtered by category, city, and state.

---

## Architecture Overview

```
                           ┌─────────────────────────────┐
  GET /api/v1/search/... ──► │     Go HTTP Server       │
                           │  searchHandler             │
                           └──────────┬──────────────────┘
                                      │
                   ┌──────────────────┼──────────────────┐
                   │                  │                   │
         (Weaviate)          (Pinecone)         (PostgreSQL)
                   │                  │                   │
       ┌──────────▼──────────┐ ┌─────▼──────────┐ ┌─────▼──────────┐
       │  Weaviate Client    │ │ Pinecone Client│ │  PG Client      │
       └──────────┬──────────┘ └─────┬──────────┘ └─────┬──────────┘
                  │ GraphQL           │ REST+gRPC          │ SQL
       ┌──────────▼──────────┐ ┌─────▼──────────┐ ┌─────▼──────────┐
       │  Weaviate           │ │  Pinecone Cloud │ │  PostgreSQL +   │
       │  Collection: Yelp   │ │  Namespace: yelp │ │  pgvector       │
       └─────────────────────┘ └─────────────────┘ └────────────────┘
                   │                  │                   │
                   └──────────────────┼──────────────────┘
                                      │
                           (Ollama for Embeddings)
```

**Data flow at index time:**
1. `index` CLI command reads the Yelp archive (reviews + business metadata)
2. For each review, business metadata (`city`, `state`, `categories`, `businessName`) is joined in-memory
3. Review text is chunked (800 chars, 100 overlap) and each chunk is embedded via Ollama
4. Reviews are upserted to Postgres (structured data) and chunks to the vector store (Weaviate/Pinecone/pgvector) in batches of 512
5. The vector store stores each chunk with its embedding and denormalized metadata; Postgres stores the full review text

**Data flow at query time:**
1. `GET /api/v1/search/businesses/{query}` arrives at the server
2. Server calls the search backend with the query string (embedded locally via Ollama)
3. Top matching chunks are returned with certainty/similarity scores
4. For Weaviate/Pinecone: business metadata comes from denormalized chunk properties; full review text from Postgres
5. Server aggregates scores by `businessId` using Top-K average (see below)
6. Top `limit` restaurant IDs are returned, ranked by aggregated score

---

## Setup

### Prerequisites
- Docker and Docker Compose

### Start Search Infrastructure

#### Option A: Weaviate (Local)
Run Weaviate and the transformers sidecar using `docker compose up -d`. Weaviate handles vectorization internally by calling the sidecar.

#### Option B: Pinecone (Cloud)
Pinecone requires an external vectorizer. You must run `ollama serve` and pass `--ollama-url` and `--ollama-model` to the Go server so it can generate embeddings via Ollama's API before sending them to Pinecone.

---

## Indexing

Populate the vector store from the Yelp archive. The command joins review
and business data, chunks and embeds each review, and upserts to the search backend:

```bash
# Weaviate (default)
go run . index --weaviate localhost:8090

# PostgreSQL/pgvector
go run . index --postgres-search --postgres-dsn "postgres://user:pass@localhost:5432/fodmap?sslmode=disable"
```

Flags:

| Flag | Default | Description |
|---|---|---|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--batch-size` | `512` | Reviews per batch upsert |
| `--workers` | `4` | Concurrent batch upload goroutines |
| `--ollama-url` | `http://localhost:11434` | Ollama server URL |
| `--ollama-model` | `nomic-embed-text` | Ollama embedding model |
| `--archive` | `../data/yelp_dataset.tar` | Path to the Yelp dataset TAR archive |
| `--checkpoint` | `index.checkpoint` | Checkpoint file path (empty string to disable) |
| `--start-offset` | `0` | Start offset for resuming indexing |
| `--filter-city` | `""` | Only index reviews for this city |

The command is **idempotent** — each review and chunk is assigned a deterministic UUID, so re-running `index` updates existing records rather than creating duplicates.

Progress is logged every batch:
```
INFO indexed batch total=512
INFO indexed batch total=1024
...
```

---

## Querying

### Endpoint

```
GET /api/v1/search/businesses/{query}[?category=<cat>][&city=<city>][&state=<state>][&limit=<n>]
GET /api/v1/search/reviews/{query}[?business_id=<id>][&limit=<n>]
GET /api/v1/search/fodmap/{ingredient}
```

### Parameters

| Parameter | Where | Required | Default | Description |
|---|---|---|---|---|
| `<query>` | path | Yes | — | Natural-language description of the restaurant or review text |
| `category` | query | No | — | Substring match against Yelp categories (e.g. `Italian`, `Tacos`) |
| `city` | query | No | — | Substring match against city (e.g. `Phoenix`) |
| `state` | query | No | — | Exact state abbreviation match (e.g. `AZ`) |
| `business_id` | query | No | — | Filter reviews by business ID |
| `limit` | query | No | `10` | Maximum number of results to return |
| `alpha` | query | No | `0` | Hybrid search weight: `0`=pure vector, `0.75`=balanced, `1`=pure keyword |

### Response

```json
{
  "business_ids": ["abc123", "def456", "ghi789"]
}
```

### Examples

```bash
# Basic semantic business search
curl "localhost:8081/api/v1/search/businesses/cozy%20Italian%20with%20great%20pasta"

# Filter by category and location
curl "localhost:8081/api/v1/search/businesses/best%20tacos?category=Mexican&city=Las%20Vegas&state=NV&limit=5"

# Find romantic dinner spots in Las Vegas
curl "localhost:8081/api/v1/search/businesses/romantic%20dinner%20candlelit?city=Las%20Vegas&state=NV"

# Search reviews for a specific business
curl "localhost:8081/api/v1/search/reviews/gluten%20free?business_id=abc123"

# FODMAP ingredient lookup
curl "localhost:8081/api/v1/search/fodmap/garlic"
```

### Server startup with search enabled

```bash
go run . serve --weaviate localhost:8090
```

If `--weaviate` is omitted, the server starts normally but `GET /search/<query>` returns `503 Service Unavailable`.

---

## Configuration Reference

### `serve` flags

| Flag | Default | Description |
|---|---|---|
| `--weaviate` | `""` | Weaviate host:port (empty disables Weaviate search) |
| `--weaviate-scheme` | `http` | Weaviate scheme (http or https) |
| `--weaviate-api-key` | `""` | Weaviate API key |
| `--pinecone-api-key` | `""` | Pinecone API key |
| `--pinecone-index-host` | `""` | Pinecone host URL |
| `--postgres-search` | `false` | Enable PostgreSQL/pgvector search backend |
| `--postgres-dsn` | `""` | PostgreSQL DSN (required if `--postgres-search`) |
| `--ollama-url` | `http://localhost:11434` | Ollama server URL |
| `--ollama-model` | `nomic-embed-text` | Ollama embedding model |
| `--chat-model` | `gemini-3-flash-preview` | Gemini model for chat |
| `--filter-model` | `gemini-3.1-flash-lite-preview` | Gemini model for topic screening |
| `--store-type` | `sqlite` | Auth store type: `sqlite` or `postgres` |
| `--port` | `8081` | HTTP server port |

### `index` flags

| Flag | Default | Description |
|---|---|---|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--batch-size` | `512` | Reviews per batch |
| `--workers` | `4` | Concurrent upload goroutines |
| `--archive` | `../data/yelp_dataset.tar` | Path to Yelp dataset TAR archive |
| `--ollama-url` | `http://localhost:11434` | Ollama server URL |
| `--ollama-model` | `nomic-embed-text` | Ollama embedding model |
| `--filter-city` | `""` | Only index reviews matching this city |
| `--checkpoint` | `index.checkpoint` | Checkpoint file (empty string to disable) |
| `--start-offset` | `0` | Resume from a known offset |

---

## Design Decisions

### Score Aggregation: Top-K Average

Each review in Weaviate has its own embedding. A query returns many individual reviews. The
challenge is aggregating per-review similarity scores into a per-restaurant ranking.

Four strategies were considered:

| Strategy | Mechanism | Volume bias | Outlier risk | Verdict |
|---|---|---|---|---|
| **Sum** | Total similarity across all matching reviews | High — chains with 500 mediocre matches beat boutiques with 3 great ones | Medium | Rejected: popular restaurants dominate regardless of quality |
| **Max** | Best single review's similarity | None | High — one lucky review can carry a poor restaurant | Rejected: not robust |
| **Average** | Mean similarity across all matching reviews | Reverse — penalizes restaurants with many reviews | Low | Rejected: high-volume restaurants are unfairly disadvantaged |
| **Top-K average** *(chosen)* | Average of the top K best-matching reviews | None | Low — requires K consistently good reviews to compete | **Chosen** |

**Top-K average (K=5)** is the right balance:
- A restaurant with 1,000 reviews isn't penalized for having many off-topic reviews (unlike average)
- A restaurant can't be carried by a single well-worded review (unlike max)
- Popular restaurants with genuinely consistent relevance rise to the top
- K=5 is tunable via the `topKReviews` constant in `search/weaviate.go`

**Example:** Query "romantic candlelit dinner"
- Restaurant A: 3 reviews scoring [0.92, 0.88, 0.85] → top-5 avg = 0.88
- Restaurant B: 50 reviews, top 5 scoring [0.91, 0.87, 0.83, 0.80, 0.78] → top-5 avg = 0.84
- Restaurant A wins despite fewer reviews — it's more consistently romantic

---

### Restaurant Representation: One Object per Review

Three approaches were considered for representing multi-review restaurants in Weaviate:

| Approach | Pros | Cons | Verdict |
|---|---|---|---|
| **One object per review** *(chosen)* | Works with auto-vectorizer, preserves review granularity | Requires client-side score aggregation | **Chosen** |
| Concatenated profile | One object per restaurant; simpler query | Token limits hit for busy restaurants (1000+ reviews); loses review-level signal | Rejected |
| Average embedding | True restaurant-level vector | Cannot use Weaviate auto-vectorizer — requires external embedding + manual averaging | Rejected |

The concatenated and average approaches both break Weaviate's built-in `text2vec-transformers`
auto-vectorization, requiring a separate embedding pipeline. One-object-per-review is the only
approach that keeps the pipeline simple while still enabling restaurant-level ranking.

---

### Vector Database: Weaviate

| Database | Built-in embedding | Filtering | Go client | Setup complexity | Notes |
|---|---|---|---|---|---|
| **Weaviate** *(chosen)* | Yes (`text2vec-transformers`) | Strong | Official | Medium (2 containers) | No-code embedding pipeline |
| pgvector | No | Full SQL WHERE | `pgx` | Low (1 container) | Good for Postgres shops; structured + vector in one DB |
| Qdrant | No | Strong (payload filters) | Official | Low (Rust, 1 container) | Best raw vector performance |
| Milvus | No | Good | Official | High (etcd + MinIO) | Production scale; overkill for local dev |
| Redis Stack | No | Moderate | Official | Low | Familiar ops; weaker vector quality |
| In-memory (flat file) | No | Manual | None | None | Prototyping only; O(N) scan |

**Decision:** Weaviate's `text2vec-transformers` module is the decisive advantage for local-first development. It removes the need for explicit embedding code. 

However, for managed cloud deployments, we now support **Pinecone**. Pinecone is better suited for production scale but requires client-side embedding, which we handle via Ollama.

---

### Transformer Model: `multi-qa-MiniLM-L6-cos-v1`

Weaviate's `text2vec-transformers` module delegates to a separate inference container. Model options:

| Model | Params | Download size | CPU speed | Quality | Best for |
|---|---|---|---|---|---|
| **multi-qa-MiniLM-L6-cos-v1** *(chosen)* | 22M | ~90 MB | Fast | Good | Asymmetric search: short query → long document |
| all-MiniLM-L6-v2 | 22M | ~90 MB | Fast | Good | General sentence similarity; not search-tuned |
| all-mpnet-base-v2 | 109M | ~420 MB | Moderate | Better | Higher quality; GPU recommended |
| paraphrase-multilingual-mpnet-base-v2 | 278M | ~1.1 GB | Slow on CPU | Best + multilingual | Non-English reviews |

**Decision:** This service uses **asymmetric search** — a short query ("great tacos near downtown")
is matched against long review documents. `multi-qa-MiniLM-L6-cos-v1` is specifically trained for
this pattern (Multi-QA = question/query to document), unlike the `all-*` models which optimize for
symmetric similarity (document to document). It also runs adequately on CPU at ~90 MB.

To upgrade to higher quality, swap the image in `docker-compose.yml`:
```yaml
image: semitechnologies/transformers-inference:sentence-transformers-all-mpnet-base-v2
```
Note: `all-mpnet-base-v2` is ~5× larger and significantly slower on CPU — a GPU is recommended.

---

### Vectorizer: Ollama vs external API

| Option | Pros | Cons |
|---|---|---|
| **Ollama** *(chosen)* | Fully local, no API key, very fast | Requires Ollama running in the background |
| Google Gemini embedding API | High quality, already integrated in codebase | API cost per embedding, rate limits, external dependency, requires manual embed-then-store code |
| OpenAI embedding API | Industry standard, high quality | New API key required, cost, external dependency |

**Decision:** `Ollama` keeps the entire pipeline local and provides incredible performance. We bypass Weaviate's auto-vectorizer to use Ollama directly for maximum batching throughput and model flexibility.

---

## Testing

The search clients (`weaviate.go` and `pinecone.go`) are tested using `httptest.Server` to mock the vector database APIs. This ensures that logic like schema enforcement, batching, and score aggregation is verified without requiring a running database.

**Example: Mocking Weaviate GraphQL**
```go
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if r.URL.Path == "/v1/graphql" {
        // Return a mocked GraphQL response
        json.NewEncoder(w).Encode(map[string]any{"data": ...})
    }
}))
```

See `search/weaviate_test.go` for full implementation details.
