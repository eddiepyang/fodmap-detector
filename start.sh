#!/usr/bin/env bash
set -e

# Clean up background processes on exit
cleanup() {
    echo ""
    echo "Shutting down..."
    kill $SERVER_PID 2>/dev/null || true
    pkill -f "fodmap-detector serve" 2>/dev/null || true
    wait 2>/dev/null
    echo "Done."
}
trap cleanup EXIT INT TERM

echo "======================================"
echo " Starting FODMAP Detector Services"
echo "======================================"

# 1. Start Weaviate in Docker
echo "[1/2] Starting Weaviate in Docker..."
docker compose up -d

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

# 2. Setup llama-go bindings if needed
if [ ! -f "llama-go/libbinding.a" ]; then
    echo "    Building llama-go C++ bindings (this may take a few minutes)..."
    make setup-llama
fi

# 3. Start the Go server in the background
echo "[2/2] Starting Go server on port 8081 with native vectorizer..."
go run -tags llamago . serve --model-path models/nomic-embed-text-v1.5.Q5_K_M.gguf &
SERVER_PID=$!

echo ""
echo "======================================"
echo " All services running!"
echo "  Weaviate:   localhost:8090"
echo "  Go Server:  localhost:8081"
echo ""
echo " Run the chat app in another terminal:"
echo "   GOOGLE_API_KEY=\$GEMINI_KEY go run -tags llamago . chat \"noodles\" --city Philadelphia --state PA"
echo ""
echo " Press Ctrl+C to stop all services."
echo "======================================"

# Wait for background process to exit
wait
