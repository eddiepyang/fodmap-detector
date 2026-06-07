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
# Basic scrape (HTML to Markdown via LLM)
go run . scrape "https://example-restaurant.com/menu" --weaviate localhost:8090

# Scrape an image-based menu or PDF using a local Vision LLM (e.g. Qwen3.6 via Ollama)
go run . scrape "https://example-restaurant.com/menu.pdf" \
  --weaviate localhost:8090 \
  --llm-backend openai-compat \
  --llm-url http://localhost:11434 \
  --llm-model qwen3.6:35b-mlx \
  --enable-vision
```

| Flag | Default | Description |
|------|---------|-------------|
| `--weaviate` | `localhost:8090` | Weaviate host:port |
| `--llm-backend` | `openai-compat` | `openai-compat` or `gemini` |
| `--llm-url` | `http://localhost:11434` | Base URL for openai-compat backend |
| `--llm-model` | `qwen3.6:35b-mlx` | LLM model to use |
| `--enable-vision` | `false` | Send PDFs/images to the vision LLM instead of text extraction |
| `--pdftotext` | `false` | Fall back to system `pdftotext` (poppler) for PDF text extraction |

##### Chat (interactive FODMAP/allergen agent)

```sh
# Find the top Thai restaurant in Las Vegas and start a chat about its dishes
GOOGLE_API_KEY=${GEMINI_KEY} go run . chat "pad thai" --city "Las Vegas" --state NV
```

See [chat.md](chat.md) for design decisions and tradeoffs.

---

