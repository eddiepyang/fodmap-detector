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
                          │  (in-process llama-go)       │
                          └──────────┬──────────────────┘
                                     │
                  ┌──────────────────┴──────────────────┐
                  │                                     │
        (Local Path)                           (Cloud Path)
                  │                                     │
      ┌──────────▼──────────┐               ┌──────────▼──────────┐
      │  Weaviate Client    │               │  Pinecone Client    │
      └──────────┬──────────┘               └──────────┬──────────┘
                 │ GraphQL                             │ REST
      ┌──────────▼──────────┐               ┌──────────▼──────────┐
      │  Weaviate           │               │  Pinecone (Cloud)   │
      │  Collection: Yelp   │               │  Namespace: yelp    │
      └─────────────────────┘               └─────────────────────┘
```

**Data flow at index time:**
1. `index` CLI command reads `archive.tar.gz` (reviews + business metadata)
2. For each review, business metadata (`city`, `state`, `categories`) is joined in-memory
3. The Go process generates embeddings in-memory via `llama-go`
4. Both the vector and metadata are stored in Weaviate in batches

**Data flow at query time:**
1. `GET /search/<description>` arrives at the server
2. Server embeds the query locally via `llama-go`
3. Server calls Weaviate or Pinecone with the raw query vector
4. Top matching reviews are returned with scores
5. Server aggregates scores by `businessId` using Top-K average (see below)
6. Top `limit` restaurant IDs are returned, ranked by aggregated score

---

## Setup

### Prerequisites
- Docker and Docker Compose

### Start Search Infrastructure

#### Option A: Weaviate (Local)
Run Weaviate using `docker compose up -d`. Embeddings are generated locally using the `llama-go` embedder and passed directly to Weaviate.

#### Option B: Pinecone (Cloud)
Pinecone requires client-side vectors, which are generated in-process using the same `llama-go` embedder before sending them to Pinecone.

---

## Indexing

Populate Weaviate from the Yelp archive. The command reads `./data/archive.tar.gz`, joins review
and business data, and upserts to Weaviate in batches:

```bash
go run -tags llamago . index --weaviate localhost:8090 --model-path models/nomic-embed-text-v1.5.Q5_K_M.gguf
```

Flags:

| Flag | Default | Description |
|---|---|---|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--model-path` | `""` | Path to GGUF embedding model |
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
go run -tags llamago . serve --weaviate localhost:8090 --model-path models/nomic-embed-text-v1.5.Q5_K_M.gguf
```

If `--weaviate` is omitted, the server starts normally but `GET /search/<query>` returns `503 Service Unavailable`.

---

## Configuration Reference

### `serve` flags

| Flag | Default | Description |
|---|---|---|
| `--weaviate` | `""` | Weaviate host:port |
| `--pinecone-api-key` | `""` | Pinecone API key |
| `--pinecone-index-host` | `""` | Pinecone host URL |
| `--model-path` | `""` | Path to GGUF embedding model |

### `index` flags

| Flag | Default | Description |
|---|---|---|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--model-path` | `""` | Path to GGUF embedding model |
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
| **One object per review** *(chosen)* | Cleanest mapping to dataset, preserves review-level signal and granularity | Requires client-side score aggregation | **Chosen** |
| Concatenated profile | One object per restaurant; simpler query | Token limits hit for busy restaurants (1000+ reviews); loses review-level signal | Rejected |
| Average embedding | True restaurant-level vector | Requires external embedding + manual averaging on every update | Rejected |

The concatenated and average approaches were initially evaluated, but one-object-per-review is the only
approach that keeps the pipeline simple while still enabling restaurant-level ranking.

---

### Vector Database: Weaviate

| Database | Filtering | Go client | Setup complexity | Notes |
|---|---|---|---|---|
| **Weaviate** *(chosen)* | Strong | Official | Medium | Used as our primary backend |
| pgvector | Full SQL WHERE | `pgx` | Low (1 container) | Good for Postgres shops; structured + vector in one DB |
| Qdrant | Strong (payload filters) | Official | Low (Rust, 1 container) | Best raw vector performance |
| Milvus | Good | Official | High (etcd + MinIO) | Production scale; overkill for local dev |
| Redis Stack | Moderate | Official | Low | Familiar ops; weaker vector quality |
| In-memory (flat file) | Manual | None | None | Prototyping only; O(N) scan |

**Decision:** Weaviate is chosen for local development due to strong filtering capabilities and its robust REST/GraphQL APIs. 

However, for managed cloud deployments, we now support **Pinecone**. Pinecone is better suited for production scale but requires client-side embedding, which we handle natively in the Go application.

---

### Transformer Model: Nomic Embed Text v1.5

We handle vectorization natively via `llama-go`.

**Decision:** This service uses **asymmetric search** — a short query ("great tacos near downtown")
is matched against long review documents. We use `nomic-embed-text-v1.5.Q5_K_M.gguf` because it provides excellent asymmetrical search quality with a relatively small footprint. It is loaded natively in-process via `llama.cpp`, which automatically offloads layers to available GPUs (Metal on Mac, CUDA on Linux) to accelerate embedding.

---

### Vectorizer: Native Go In-Process vs External API

| Option | Pros | Cons |
|---|---|---|
| **llama-go (Native)** *(chosen)* | Fully local, no API key, high performance, uses native GPU, no external service required | Requires local model download (~800MB) |
| Google Gemini embedding API | High quality, already integrated in codebase | API cost per embedding, rate limits, external dependency |
| OpenAI embedding API | Industry standard, high quality | New API key required, cost, external dependency |

**Decision:** Using `llama-go` in-process keeps the entire pipeline local and eliminates IPC/HTTP overhead. The only trade-off is downloading the GGUF model once, but the single-binary deployment and native GPU acceleration make it the ideal choice.

#### Compiling llama-go (macOS / CGO Notes)

The migration to `llama-go` requires compiling the underlying `llama.cpp` C++ library using CGO. The compilation process had several platform-specific hurdles on macOS that are now automated by the `make setup-llama` target:

1. **Missing `cmake`**: `llama.cpp` relies on CMake to generate its build files. You must install it (e.g., `brew install cmake`) before running the build.
2. **Hardcoded `.so` vs `.dylib` extensions**: `llama-go`’s Makefile assumes it is running on Linux. When building shared libraries by default, it tries to copy `libllama.so` at the end of the build. However, on macOS, the compiler generates `libllama.dylib` files instead, causing the `make` script to crash.
3. **Switching to Static Libraries (`.a`)**: To avoid the `.so` bug, the project's `Makefile` now passes `CMAKE_ARGS="-DBUILD_SHARED_LIBS=OFF"`. This forces `llama-go` to build static libraries (`.a` files), which are cross-platform compatible and preferable for Go binaries anyway.
4. **Missing C++17 Standard Flag**: macOS’s Apple Clang defaults to an older C++ standard during CGO execution. Since `llama.cpp` requires `std::variant` (introduced in C++17), compiling it without explicit flags throws dozens of syntax errors. To fix this seamlessly for all users, `#cgo CXXFLAGS: -std=c++17` has been directly embedded into the `llama-go` Go source files.
5. **Auto-linking Metal Frameworks**: In order to run the built embeddings on the GPU locally on Mac, it requires linking multiple Apple specific frameworks (`-framework Accelerate -framework Foundation -framework Metal -framework MetalKit`). To save developers from manually appending these `CGO_LDFLAGS` to every build command, they have been embedded directly in the `llama-go` source files utilizing the `darwin` constraint (`#cgo darwin LDFLAGS`), ensuring the project remains clean and entirely cross-platform.
6. **Missing Static Libraries in the Root Folder**: After compiling the static libraries, the Go linker (`ld`) can fail with `Undefined symbols for architecture arm64: _ggml_backend_metal_reg`. This happens because `llama-go`'s Makefile compiles the Metal (`libggml-metal.a`) and BLAS (`libggml-blas.a`) static libraries, but neglects to copy them out of the `build/` folder.
7. **The Final Fix**: To solve all of these issues, the `setup-llama` target in `fodmap-detector/Makefile` uses a `find` command to automatically extract and copy all generated `.a` files into the `llama-go` root directory. Once all libraries are present, `go build -tags llamago` successfully links them into the final binary.

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
