#!/bin/bash

echo "==> Stopping any running indexing processes..."
pkill -f "go run . index" || true
pkill -f "fodmap-detector index" || true

echo "==> Stopping Ollama..."
# Try systemctl first if it exists, otherwise pkill
if systemctl is-active --quiet ollama; then
    sudo systemctl stop ollama
fi
pkill -f "ollama serve" || true
# Kill any processes still holding port 11434
sudo lsof -t -i:11434 | xargs -r sudo kill -9

echo "==> Starting Ollama in the background..."
export OLLAMA_HOST="127.0.0.1"
export OLLAMA_NUM_PARALLEL=4
ollama serve > /dev/null 2>&1 &

echo "==> Waiting for Ollama to be ready..."
sleep 3
for i in {1..10}; do
    if curl -s -o /dev/null http://localhost:11434/api/tags; then
        echo "Ollama is ready!"
        break
    fi
    sleep 1
done

echo "==> Ensuring embedding model is pulled..."
ollama pull nomic-embed-text

echo "==> Resuming indexing..."
# It will automatically pick up from index.checkpoint if it exists
go run . index --weaviate localhost:8090
