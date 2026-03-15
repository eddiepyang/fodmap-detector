# Restaurant Search Service

Semantic search over Yelp restaurant reviews using Weaviate and locally-run transformer embeddings.
Given a natural-language description, the endpoint returns restaurant (business) IDs ranked by how
well their reviews match the query — optionally filtered by category, city, and state.

---

## Architecture Overview

```
                          ┌─────────────────────────────┐
  GET /search/...    ───► │     Go HTTP Server           │
                          │  searchHandler               │
                          │    ↓ nearText query          │
                          │  Weaviate Client             │
                          └──────────┬──────────────────┘
                                     │ GraphQL (port 8090)
                          ┌──────────▼──────────────────┐
                          │  Weaviate                    │
                          │  Collection: YelpReview      │
                          │  (one object per review)     │
                          └──────────┬──────────────────┘
                                     │ HTTP inference API
                          ┌──────────▼──────────────────┐
                          │  t2v-transformers            │
                          │  multi-qa-MiniLM-L6-cos-v1  │
                          └─────────────────────────────┘
```

**Data flow at index time:**
1. `index` CLI command reads `archive.tar.gz` (reviews + business metadata)
2. For each review, business metadata (`city`, `state`, `categories`) is joined in-memory
3. Reviews are sent to Weaviate in batches of 100
4. Weaviate calls the transformer sidecar to generate a vector for the `text` field
5. Both the vector and metadata are stored in Weaviate

**Data flow at query time:**
1. `GET /search/<description>` arrives at the server
2. Server calls Weaviate `nearText` with the query string (Weaviate embeds the query too)
3. Top matching reviews are returned with `certainty` scores
4. Server aggregates scores by `businessId` using Top-K average (see below)
5. Top `limit` restaurant IDs are returned, ranked by aggregated score

---

## Setup

### Prerequisites
- Docker and Docker Compose

### Start Weaviate

```bash
docker compose up -d
```

Two containers start:
- **weaviate** — vector database, REST/GraphQL on host port `8090`
- **t2v-transformers** — transformer inference sidecar (internal to Docker network)

**First run:** the transformer image downloads the `multi-qa-MiniLM-L6-cos-v1` model (~90 MB).
Wait for this log line before running `index`:

```
t2v-transformers  | Application startup complete.
```

Check readiness:
```bash
docker compose logs t2v-transformers | grep "startup complete"
```

---

## Indexing

Populate Weaviate from the Yelp archive. The command reads `./data/archive.tar.gz`, joins review
and business data, and upserts to Weaviate in batches:

```bash
go run ./cmd/cli index --weaviate localhost:8090
```

Flags:

| Flag | Default | Description |
|---|---|---|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--batch-size` | `100` | Reviews per Weaviate batch upsert |

The command is **idempotent** — each review is assigned a deterministic UUID from its `review_id`,
so re-running `index` updates existing records rather than creating duplicates.

The Yelp dataset has ~7 million reviews. Progress is logged every batch:
```
INFO indexed batch total=100
INFO indexed batch total=200
...
INFO indexing complete total_reviews=6990280
```

---

## Querying

### Endpoint

```
GET /search/<description>[?category=<cat>][&city=<city>][&state=<state>][&limit=<n>]
```

### Parameters

| Parameter | Where | Required | Default | Description |
|---|---|---|---|---|
| `<description>` | path | Yes | — | Natural-language description of the restaurant |
| `category` | query | No | — | Substring match against Yelp categories (e.g. `Italian`, `Tacos`) |
| `city` | query | No | — | Exact city name match (e.g. `Phoenix`) |
| `state` | query | No | — | Exact state abbreviation match (e.g. `AZ`) |
| `limit` | query | No | `10` | Maximum number of restaurant IDs to return |

### Response

```json
{
  "business_ids": ["abc123", "def456", "ghi789"]
}
```

### Examples

```bash
# Basic semantic search
curl "localhost:8080/search/cozy Italian with great pasta"

# Filter by category and location
curl "localhost:8080/search/great tacos?category=Mexican&city=Phoenix&state=AZ&limit=5"

# Find romantic dinner spots in Las Vegas
curl "localhost:8080/search/romantic dinner candlelit?city=Las Vegas&state=NV"
```

### Server startup with search enabled

```bash
go run . serve --weaviate localhost:8090
```

If `--weaviate` is omitted, the server starts normally but `GET /search/<query>` returns `503 Service Unavailable`.

---

## Configuration Reference

### `serve` flags (relevant to search)

| Flag | Default | Description |
|---|---|---|
| `--weaviate` | `""` (disabled) | Weaviate host:port; omit to disable search endpoint |

### `index` flags

| Flag | Default | Description |
|---|---|---|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--batch-size` | `100` | Reviews per batch |

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

**Decision:** Weaviate's `text2vec-transformers` module is the decisive advantage. The `index`
command sends raw review text; Weaviate calls the transformer container and stores the vector
automatically. Every alternative requires an explicit embed-then-store pipeline (call API, receive
float arrays, insert with vector). That pipeline makes sense at production scale but is unnecessary
complexity here.

If an external embedding API (Gemini, OpenAI) were already in use, **Qdrant** would be the better
choice: simpler ops, better raw performance, no sidecar container.

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

### Vectorizer: Weaviate built-in vs external API

| Option | Pros | Cons |
|---|---|---|
| **text2vec-transformers** *(chosen)* | Fully local, no API key, auto-vectorizes on insert | Extra Docker container; first-run model download |
| Google Gemini embedding API | High quality, already integrated in codebase | API cost per embedding, rate limits, external dependency, requires manual embed-then-store code |
| OpenAI embedding API | Industry standard, high quality | New API key required, cost, external dependency |

**Decision:** `text2vec-transformers` keeps the entire pipeline local and removes the need for any
explicit embedding code. The only trade-off is the extra Docker container and ~90 MB model download,
which is a one-time cost.
