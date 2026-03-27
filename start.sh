#!/usr/bin/env bash
set -e

# Clean up background processes on exit
cleanup() {
    echo ""
    echo "Shutting down..."
    kill $VECTORIZER_PID 2>/dev/null || true
    kill $SERVER_PID 2>/dev/null || true
    pkill -f "fodmap-detector serve" 2>/dev/null || true
    wait 2>/dev/null
    echo "Done."
}
trap cleanup EXIT INT TERM

echo "======================================"
echo " Starting FODMAP Detector Services"
echo "======================================"

# 1. Start Weaviate
echo "[1/3] Starting Weaviate in Docker..."
docker compose up -d

# 2. Start the Python Vectorizer in the background
echo "[2/3] Starting Python Vectorizer on port 8080..."
(
    cd vectorizer-proxy
    if command -v conda &> /dev/null; then
        eval "$(conda shell.bash hook)"
        conda activate torch-env 2>/dev/null || true
    fi
    if [ -f "venv/bin/activate" ]; then
        source venv/bin/activate
    fi
    uvicorn app:app --host 0.0.0.0 --port 8080
) &
VECTORIZER_PID=$!

# Wait for the vectorizer to become healthy
echo "    Waiting for vectorizer to be ready..."
for i in $(seq 1 30); do
    if curl -s -o /dev/null http://localhost:8080/.well-known/ready 2>/dev/null; then
        echo "    Vectorizer is ready!"
        break
    fi
    if ! kill -0 $VECTORIZER_PID 2>/dev/null; then
        echo "    ERROR: Vectorizer process died."
        exit 1
    fi
    sleep 1
done

# 3. Start the Go server in the background
echo "[3/3] Starting Go server on port 8081..."
go run . serve &
SERVER_PID=$!

echo ""
echo "======================================"
echo " All services running!"
echo "  Weaviate:   localhost:8090"
echo "  Vectorizer: localhost:8080"
echo "  Go Server:  localhost:8081"
echo ""
echo " Run the chat app in another terminal:"
echo "   GEMINI_API_KEY=\$GEMINI_KEY go run . chat \"noodles\" --city Philadelphia --state PA"
echo ""
echo " Press Ctrl+C to stop all services."
echo "======================================"

# Wait for either background process to exit
wait
