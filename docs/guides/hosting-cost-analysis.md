# Hosting & Cost Analysis

How to host the FODMAP Detector backend + scraper + frontend cost-effectively,
and why a single dedicated box beats managed cloud and Kubernetes for this
workload at the current scale (gigabytes of source data, **under ~5 M embedded
vectors**).

## What actually has to run

| Component | Role | Always-on? |
| --- | --- | --- |
| Go binary (`fodmap-detector serve`) | HTTP API **+ River workers** (discovery / scrape / menutracking) run in-process | Yes (single static binary, ~300 MB RAM) |
| Postgres + pgvector | Relational data + River job queue, optionally the vector index | Yes |
| Weaviate | Vector index (alternative to pgvector) | Optional |
| Ollama (`nomic-embed-text`) | 768-dim embeddings — needed at **query time**, not just indexing | Effectively yes (~2 GB when loaded) |
| Gemini API | Chat + discovery | External, pay-per-use |
| Python scraper (`../scraper`) | PDF / OCR / JS-render | On-demand only (`START_SCRAPER=false` by default) |
| Frontend (separate repo) | Built React SPA — static files behind a reverse proxy | Yes (negligible RAM) |

The two genuinely always-on, stateful pieces are the **Go binary** and **one
Postgres**. The scraper is queue-driven and batch; the Python service is opt-in.

## The cost driver at GB scale: the vector index in RAM

HNSW indexes live in RAM. For both Weaviate and pgvector:

> **RAM ≈ N vectors × D dims × 4 bytes × 2** (graph overhead)

At 768-dim embeddings:

| Embedded chunks | float32 (default) | halfvec / float16 | Binary quantized |
| --- | --- | --- | --- |
| 1 M | ~6 GB | ~3 GB | ~0.5 GB |
| 2 M | ~12 GB | ~6 GB | ~1 GB |
| 5 M | ~30 GB | ~15 GB | ~2.5 GB |

Raw source data on disk is cheap NVMe; **RAM for the index is the cost.**

### Highest-ROI change: quantize from day one

- **pgvector → `halfvec(768)`** (float16): ~50 % less index RAM/storage at
  negligible recall loss. The migrations currently use `vector(768)`
  (`internal/db/migrations/000001_baseline.up.sql`); switching to
  `halfvec(768)` with `halfvec_cosine_ops` indexes roughly halves the box you
  rent. Retrofitting quantization onto a large full-precision index later is
  painful — do it before loading.
- **Weaviate → BQ/PQ**: binary quantization with re-ranking cuts the in-RAM
  footprint up to ~12–32×, keeping full vectors on disk for the re-rank step.

## Two layouts (under ~5 M vectors)

Use **one** vector store, not both. Postgres always runs (it owns the River
queue regardless).

### Layout A — pgvector + halfvec (one fewer service)

Postgres holds relational data, the River queue, **and** the vector index.

- RAM: base services ~4 GB + index (~6 GB at 2 M, ~15 GB at 5 M with halfvec).
- Simplest and cheapest **up to ~2–3 M vectors** (fits a 16 GB box). At the 5 M
  end the halfvec index pushes you to a 32 GB box.
- Drop the Weaviate container entirely.

### Layout B — Weaviate + BQ (heavier compression)

Weaviate holds the vector index (BQ-compressed in RAM, full vectors on disk);
Postgres holds only relational data + River.

- RAM: base services ~4 GB + Weaviate ~3–4 GB even at 5 M (BQ).
- Fits a **16 GB box up to ~5 M** because BQ compresses far harder than
  halfvec — at the top of this range Weaviate needs a smaller box than pgvector.
- Adds a service and ops surface, but the compression earns its keep as you grow.

**Rule of thumb:** at 1–2 M vectors, prefer pgvector (one fewer service); near
5 M and growing, Weaviate+BQ saves a RAM tier and scales further.

## Recommended host: one dedicated box

Hetzner raised **cloud** (CPX/CCX) prices 2.1–2.7× on 15 June 2026, so high-RAM
cloud instances are now €200–300/mo. The **dedicated AX line barely moved** and
is where big RAM is cheap:

| Need | Box | RAM | Approx €/mo |
| --- | --- | --- | --- |
| ≤ 2–3 M (either layout) | Cloud CX/CCX | 16 GB | ~25–35 |
| 5 M, headroom, all services | **AX42 (dedicated)** | **64 GB ECC + NVMe** | **~57** |
| 5 M+ / growing | AX102+ | 128 GB+ | ~100+ |

One AX42 runs Postgres+pgvector (or Weaviate), Ollama, the Go server, the
frontend container, and a reverse proxy — with room to grow well past 5 M.

## Why not GCP

For this steady, stateful, memory-bound workload GCP is the expensive choice:

- **Compute is ~3× pricier for the same RAM** — `e2-standard-2` (8 GB) ≈
  $49/mo vs a Hetzner 8 GB box at ~$15; a 64 GB GCP memory instance is
  $300–450/mo vs €57 for an AX42.
- **The workload defeats scale-to-zero** — River workers must be always-on
  (`min-instances ≥ 1`, billed 24/7); Weaviate has no managed GCP service and
  holds its index in RAM; Ollama needs an always-on GPU (~$0.67/hr ≈ $480/mo)
  or plain always-on CPU. Three resident, stateful services is the anti-pattern
  for serverless billing.
- **Decomposing into managed pieces costs more** — Cloud SQL (usable tier) +
  a VM for Weaviate/Ollama + Cloud Run easily reaches $80–120/mo plus egress,
  versus ~€15–57 on one box, while adding VPC/IAM/Artifact Registry complexity.

GCP wins only when you need HA/failover, autoscaling for spiky public traffic,
compliance posture, or in-region Vertex/Gemini latency — none of which a
single-tenant steady index requires.

## Why Kubernetes raises costs here

Kubernetes adds cost without buying anything this workload needs:

- **Control-plane fee** — managed K8s (GKE/EKS) charges ~$0.10/hr (~$73/mo) per
  cluster on top of the nodes, before running a single container.
- **Node overhead** — you still rent the same RAM for the index, plus headroom
  for the kubelet, system pods, and a load balancer (~$15–20/mo each on cloud).
- **Stateful friction** — Postgres, Weaviate, and Ollama are stateful and
  memory-resident. K8s shines at scheduling stateless, horizontally-scaled
  replicas; here you'd run single StatefulSets with PVCs, i.e. a more complex
  way to run what `docker compose` already does on one box.
- **No autoscaling benefit** — a resident HNSW index can't scale to zero or
  spread across ephemeral pods without sharding work you don't need yet.

K8s only starts paying off with many services, multiple teams, true horizontal
autoscaling, or multi-region HA. Until then it is pure overhead — budget an
extra ~$100–150/mo and significant ops time for no functional gain. A single
box with `docker compose` (and a systemd unit or `restart: unless-stopped`) is
the right tool at this scale.

## Reference deployment (single box, docker compose)

The repo's `docker-compose.yaml` is a **dev** config (services bound to
`127.0.0.1`, no app/frontend/proxy). A production single-box deployment adds the
Go server, the frontend container, and a reverse proxy (Caddy for automatic
TLS), and keeps the datastores off the public interface.

> These are reference snippets. The Go server needs a `Dockerfile` (none exists
> yet — the dev flow uses `go run .`), and the frontend image is built in its
> own repo. Wire up the actual files once the layout is chosen.

### `Dockerfile` (Go server) — reference

```dockerfile
# Build
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/fodmap-detector .

# Run
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/fodmap-detector /fodmap-detector
ENTRYPOINT ["/fodmap-detector"]
```

### `docker-compose.prod.yaml` — Layout A (pgvector + halfvec)

```yaml
services:
  caddy:
    image: caddy:2
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
    depends_on: [server, frontend]

  frontend:
    image: fodmap-frontend:latest   # built in the frontend repo
    restart: unless-stopped
    expose: ["80"]

  server:
    build: .                        # uses the Dockerfile above
    command: ["serve"]
    restart: unless-stopped
    environment:
      ENABLE_PIPELINE: "true"
      POSTGRES_DSN: "postgres://fodmap:${POSTGRES_PASSWORD}@postgres:5432/fodmap?sslmode=disable"
      GOOGLE_API_KEY: "${GOOGLE_API_KEY}"
      JWT_SECRET: "${JWT_SECRET}"
      ADMIN_EMAIL: "${ADMIN_EMAIL}"
      OLLAMA_HOST: "http://ollama:11434"
    expose: ["8081"]
    depends_on: [postgres, ollama]

  postgres:
    image: pgvector/pgvector:pg16
    restart: unless-stopped
    environment:
      POSTGRES_USER: fodmap
      POSTGRES_PASSWORD: "${POSTGRES_PASSWORD}"
      POSTGRES_DB: fodmap
    volumes:
      - postgres_data:/var/lib/postgresql/data
    # no host port — only reachable on the compose network

  ollama:
    image: ollama/ollama:latest
    restart: unless-stopped
    volumes:
      - ollama_data:/root/.ollama

volumes:
  postgres_data:
  ollama_data:
  caddy_data:
```

### Layout B (Weaviate) — delta from Layout A

Add the Weaviate service and point the server at it; Postgres then holds only
relational data + River.

```yaml
  weaviate:
    image: semitechnologies/weaviate:1.25.4
    restart: unless-stopped
    environment:
      QUERY_DEFAULTS_LIMIT: "25"
      AUTHENTICATION_ANONYMOUS_ACCESS_ENABLED: "true"
      PERSISTENCE_DATA_PATH: "/var/lib/weaviate"
      DEFAULT_VECTORIZER_MODULE: "none"
      ENABLE_MODULES: ""
      CLUSTER_HOSTNAME: "node1"
    volumes:
      - weaviate_data:/var/lib/weaviate
    expose: ["8080"]
```

Add `WEAVIATE: "weaviate:8080"` to the `server` environment, add `weaviate` to
its `depends_on`, and add a `weaviate_data:` volume. Enable BQ on the class
schema to keep the in-RAM footprint small.

### `Caddyfile` — reference

```
your-domain.example {
    handle /api/* {
        reverse_proxy server:8081
    }
    handle {
        reverse_proxy frontend:80
    }
}
```

## Risks and Gaps

- **Vector-store parity** — confirm the pgvector search path
  (`fodmap/store/postgres.go`) is at feature parity with the Weaviate path for
  your queries before dropping Weaviate in Layout A.
- **halfvec migration** — moving `vector(768)` → `halfvec(768)` is a schema
  change with index/recall implications; validate recall on real data and do it
  before bulk-loading, not after.
- **No single point of failure protection** — one box means no HA. Take regular
  Postgres + Weaviate volume backups (off-box). If uptime SLAs matter, this is
  the trade-off vs. managed/HA options.
- **Embeddings at query time** — Ollama must stay resident for query embedding;
  budget its ~2 GB. Offloading to a hosted embedding endpoint could shrink the
  box but adds per-call cost and an external dependency.
- **Scraper bursts** — the on-demand Python scraper and OCR/vision paths can
  spike CPU/RAM; run heavy batch crawls when the index isn't under query load,
  or on a separate ephemeral box.
- **Hetzner pricing volatility** — cloud prices rose sharply in June 2026;
  re-check current AX/CX rates before committing.
```
