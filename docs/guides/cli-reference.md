# CLI Reference

### CLI

Run the CLI with:

```sh
go run .
```

#### Commands

##### Index (Weaviate)

```sh
go run . index --weaviate localhost:8090
```

| Flag | Default | Description |
|------|---------|-------------|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--batch-size` | `512` | Reviews per batch |
| `--workers` | `4` | Concurrent batch upload goroutines |
| `--archive` | `../data/yelp_dataset.tar` | Path to the Yelp dataset TAR archive |
| `--ollama-url` | `""` | Ollama server URL (e.g. `http://localhost:11434`) |
| `--ollama-model` | `""` | Ollama embedding model (e.g. `nomic-embed-text`) |

##### Scrape (Menu Extraction)

Scrape a restaurant menu page (HTML or PDF), extract the dishes and ingredients using an LLM, and index them into Weaviate for the chat agent to use.

```sh
# Basic scrape with Ollama (Mac)
go run . scrape "https://example-restaurant.com/menu" \
  --weaviate localhost:8090 \
  --llm-url http://localhost:11434/v1 \
  --llm-model qwen3.6:35b-mlx \
  --enable-vision

# Scrape with vLLM (Linux 5080)
go run . scrape "https://example-restaurant.com/menu.pdf" \
  --weaviate localhost:8090 \
  --llm-url http://localhost:8000/v1 \
  --llm-model Qwen/Qwen3-VL-8B-Instruct-AWQ \
  --enable-vision

# Scrape with OpenAI (cloud)
go run . scrape "https://example-restaurant.com/menu" \
  --weaviate localhost:8090 \
  --llm-url https://api.openai.com/v1 \
  --llm-model gpt-4o-mini \
  --llm-api-key "$OPENAI_API_KEY"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--llm-url` | `http://localhost:11434/v1` | Base URL for OpenAI-compatible LLM endpoint (must include `/v1`) |
| `--llm-model` | `qwen3.6:35b-mlx` | LLM model name |
| `--llm-api-key` | — | API key for cloud backends (OpenAI, Gemini) |
| `--llm-reasoning-effort` | `none` | Reasoning effort: `none` \| `low` \| `medium` \| `high` |
| `--enable-vision` | `false` | Send PDFs/images to the vision LLM instead of text extraction |
| `--pdftotext` | `false` | Fall back to system `pdftotext` (poppler) for PDF text extraction |
| `--ignore-robots` | `false` | Skip robots.txt check |
| `--enable-js-render` | `false` | Render JS-only pages via chromedp (requires Chrome) |
| `--store` | `weaviate` | Storage backend: `weaviate` \| `postgres` \| `pinecone` |
| `--embed-backend` | `ollama` | Embedding backend: `ollama` \| `vectorizer` |

See [LLM Serving](llm-serving.md) for full backend setup instructions and the quick-reference table.

##### Chat (interactive FODMAP/allergen agent)

```sh
# Find the top Thai restaurant in Las Vegas and start a chat about its dishes
GOOGLE_API_KEY=${GEMINI_KEY} go run . chat "pad thai" --city "Las Vegas" --state NV
```

See [chat.md](chat.md) for design decisions and tradeoffs.

---

