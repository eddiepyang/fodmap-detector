#!/usr/bin/env bash
set -e

# Clean up background processes on exit
cleanup() {
    echo ""
    echo "Shutting down..."
    if [ -n "$OLLAMA_PID" ]; then
        kill $OLLAMA_PID 2>/dev/null || true
    fi
    if [ -n "$SCRAPER_PID" ]; then
        kill $SCRAPER_PID 2>/dev/null || true
    fi
    kill $SERVER_PID 2>/dev/null || true
    pkill -f "fodmap-detector serve" 2>/dev/null || true
    wait 2>/dev/null
    echo "Done."
}
trap cleanup EXIT INT TERM

echo "======================================"
echo " Starting FODMAP Detector Services"
echo "======================================"

# 1. Start Ollama
echo "[1/4] Starting Ollama server..."
if curl -s -o /dev/null http://localhost:11434/api/tags 2>/dev/null; then
    echo "    Ollama is already running."
    OLLAMA_PID=""
else
    # Start Ollama in the background (allow up to 16 parallel concurrent models/requests)
    OLLAMA_HOST="127.0.0.1" OLLAMA_NUM_PARALLEL=16 ollama serve > /dev/null 2>&1 &
    OLLAMA_PID=$!
    
    echo "    Waiting for Ollama to be ready..."
    for i in $(seq 1 30); do
        if curl -s -o /dev/null http://localhost:11434/api/tags 2>/dev/null; then
            echo "    Ollama is ready!"
            break
        fi
        if ! kill -0 $OLLAMA_PID 2>/dev/null; then
            echo "    ERROR: Ollama process died. Is it installed?"
            exit 1
        fi
        if [ "$i" -eq 30 ]; then
            echo "    ERROR: Ollama did not become ready in time."
            exit 1
        fi
        sleep 1
    done
fi

echo "    Ensuring nomic-embed-text model is available..."
ollama pull nomic-embed-text > /dev/null 2>&1 || echo "    Warning: failed to pull nomic-embed-text model"

# 1b. Optionally start the Python scraper service for PDF/OCR + webagent.
# Set START_SCRAPER=true to enable (defaults to false — pure-Go is the default).
SCRAPER_PID=""
if [ "${START_SCRAPER:-false}" = "true" ]; then
    echo "[1b/5] Starting Python scraper service on port 8765..."
    SCRAPER_DIR="${SCRAPER_DIR:-../scraper}"
    if [ ! -d "$SCRAPER_DIR" ]; then
        echo "    ERROR: scraper dir $SCRAPER_DIR not found. Set SCRAPER_DIR or clone the repo."
        exit 1
    fi
    (cd "$SCRAPER_DIR" && uv run uvicorn scraper.app:app --port 8765 > /dev/null 2>&1) &
    SCRAPER_PID=$!
    echo "    Waiting for scraper service to be ready..."
    for i in $(seq 1 30); do
        if curl -s -o /dev/null http://localhost:8765/healthz 2>/dev/null; then
            echo "    Scraper service is ready!"
            break
        fi
        if ! kill -0 $SCRAPER_PID 2>/dev/null; then
            echo "    ERROR: scraper process died. Is 'uv sync' done in $SCRAPER_DIR?"
            exit 1
        fi
        if [ "$i" -eq 30 ]; then
            echo "    ERROR: scraper service did not become ready in time."
            exit 1
        fi
        sleep 1
    done
fi

# 2. Start Postgres and Weaviate in Docker
echo "[2/5] Starting Postgres and Weaviate in Docker..."
docker compose up -d

# Wait for Postgres to become ready (query the host port directly)
echo "    Waiting for Postgres to be ready..."
for i in $(seq 1 30); do
    if (echo > /dev/tcp/127.0.0.1/5432) >/dev/null 2>&1; then
        echo "    Postgres is ready!"
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "    ERROR: Postgres did not become ready in time."
        exit 1
    fi
    sleep 1
done

# Wait for Weaviate to become healthy
echo "    Waiting for Weaviate to be ready..."
for i in $(seq 1 60); do
    if curl -s -o /dev/null http://localhost:8090/v1/.well-known/ready 2>/dev/null; then
        echo "    Weaviate is ready!"
        break
    fi
    if [ "$i" -eq 60 ]; then
        echo "    ERROR: Weaviate did not become ready in time."
        exit 1
    fi
    sleep 2
done

# 3. Run database migrations (domain tables + river schema)
POSTGRES_DSN="${POSTGRES_DSN:-postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable}"
echo "[3/5] Running database migrations..."
POSTGRES_DSN="$POSTGRES_DSN" go run . db migrate-up

# 4. Start the Go server in the background
echo "[4/5] Starting Go server on port 8081..."
CONFLICTING_PID=$(lsof -t -i :8081 || true)
if [ -n "$CONFLICTING_PID" ]; then
    echo "    Found conflicting process(es) on port 8081: $CONFLICTING_PID. Killing..."
    kill -9 $CONFLICTING_PID 2>/dev/null || true
    sleep 1
fi
ENABLE_PIPELINE=true WEAVIATE=localhost:8090 POSTGRES_DSN="$POSTGRES_DSN" ADMIN_EMAIL="admin@example.com" go run . serve &
SERVER_PID=$!

echo ""
echo "======================================"
echo " All services running!"
echo "  Postgres:   localhost:5432"
echo "  Ollama:     localhost:11434"
echo "  Weaviate:   localhost:8090"
echo "  Go Server:  localhost:8081"
if [ -n "$SCRAPER_PID" ]; then
    echo "  Scraper:    localhost:8765 (PDF/OCR + webagent)"
fi
echo ""
echo " Run the chat app in another terminal:"
echo "   GOOGLE_API_KEY=\$GEMINI_KEY go run . chat \"noodles\" --city Philadelphia --state PA"
echo ""
echo " Scrape menus (vLLM at localhost:8000/v1 by default):"
echo "   Image menu (pure-Go vision, no service):"
echo "     go run . scrape <html-url> --enable-vision"
echo "   JS-rendered menu (headless Chrome, no service/adapter):"
echo "     go run . scrape <js-url> --enable-js-render"
if [ -n "$SCRAPER_PID" ]; then
    echo "   Via the scraper service (multi-page PDFs / per-site webagent):"
    echo "     PDF/OCR:    go run . scrape <pdf-url> --extractor-url http://localhost:8765"
    echo "     JS page:    go run . scrape <url> --extractor-url http://localhost:8765 \\"
    echo "                 --enable-js-render --webagent-adapter site/target"
fi
echo ""
echo " Press Ctrl+C to stop all services."
echo "======================================"

# Wait for background process to exit
wait