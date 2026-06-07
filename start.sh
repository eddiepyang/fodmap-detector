#!/usr/bin/env bash
set -e

# Clean up background processes on exit
cleanup() {
    echo ""
    echo "Shutting down..."
    if [ -n "$OLLAMA_PID" ]; then
        kill $OLLAMA_PID 2>/dev/null || true
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

# 2. Start Postgres and Weaviate in Docker
echo "[2/4] Starting Postgres and Weaviate in Docker..."
docker compose up -d

# Wait for Postgres to become ready (query the host port directly)
echo "    Waiting for Postgres to be ready..."
for i in $(seq 1 30); do
    if pg_isready -h 127.0.0.1 -p 5432 -U fodmap > /dev/null 2>&1; then
        echo "    Postgres is ready!"
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "    ERROR: Postgres did not become ready in time."
        echo "    Hint: install postgres-client for pg_isready, or check that docker compose is running."
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

# 3. Run menutracking migrations if POSTGRES_DSN is set
POSTGRES_DSN="${POSTGRES_DSN:-postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable}"
echo "[3/4] Running menutracking migrations..."
POSTGRES_DSN="$POSTGRES_DSN" go run . menutracking migrate-up || echo "    Warning: menutracking migrations failed (may already be up)"

# 4. Start the Go server in the background
echo "[4/4] Starting Go server on port 8081..."
ENABLE_PIPELINE=true WEAVIATE=localhost:8090 POSTGRES_DSN="$POSTGRES_DSN" STORE_TYPE=postgres go run . serve &
SERVER_PID=$!

echo ""
echo "======================================"
echo " All services running!"
echo "  Postgres:   localhost:5432"
echo "  Ollama:     localhost:11434"
echo "  Weaviate:   localhost:8090"
echo "  Go Server:  localhost:8081"
echo ""
echo " Run the chat app in another terminal:"
echo "   GOOGLE_API_KEY=\$GEMINI_KEY go run . chat \"noodles\" --city Philadelphia --state PA"
echo ""
echo " Press Ctrl+C to stop all services."
echo "======================================"

# Wait for background process to exit
wait