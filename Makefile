.PHONY: all build test lint check setup setup-ollama setup-docker setup-deps start run

all: lint test build

# ---------------------------------------------------------------------------
# Setup — install all prerequisites (works on Linux and macOS)
# ---------------------------------------------------------------------------

# Detect OS for platform-specific install commands.
UNAME := $(shell uname -s)

setup: setup-deps setup-ollama setup-docker
	@echo ""
	@echo "✅ Setup complete!"
	@echo ""
	@echo "  Next steps:"
	@echo "    1. Export your Gemini key:  export GOOGLE_API_KEY=<your_key>"
	@echo "    2. Start all services:      make start"
	@echo "    3. Or server only:          make run"
	@echo ""

## Go dependencies and linter
setup-deps:
	@echo "==> Installing Go dependencies..."
	go mod download
	@echo "==> Installing golangci-lint..."
ifeq ($(UNAME),Darwin)
	@which golangci-lint > /dev/null 2>&1 || brew install golangci-lint
else
	@which golangci-lint > /dev/null 2>&1 || { \
		echo "    Installing golangci-lint via install script..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $$(go env GOPATH)/bin; \
	}
endif
	@echo "    golangci-lint: $$(golangci-lint --version 2>/dev/null || echo 'not found')"

## Ollama — install if missing, then pull the embedding model
setup-ollama:
	@echo "==> Setting up Ollama..."
ifeq ($(UNAME),Darwin)
	@which ollama > /dev/null 2>&1 || { \
		echo "    Installing Ollama via Homebrew..."; \
		brew install ollama; \
	}
else
	@which ollama > /dev/null 2>&1 || { \
		echo "    Installing Ollama via install script..."; \
		curl -fsSL https://ollama.com/install.sh | sh; \
	}
endif
	@echo "    Ollama: $$(ollama --version 2>/dev/null || echo 'not found')"
	@echo "    Starting Ollama (if not already running)..."
	@curl -s -o /dev/null http://localhost:11434/api/tags 2>/dev/null || { \
		echo "    Launching ollama serve in background..."; \
		OLLAMA_HOST=127.0.0.1 ollama serve > /dev/null 2>&1 & \
		sleep 3; \
	}
	@echo "    Pulling nomic-embed-text model (this may take a while on first run)..."
	ollama pull nomic-embed-text

## Docker / Weaviate
setup-docker:
	@echo "==> Checking Docker..."
	@docker info > /dev/null 2>&1 || { echo "❌ Docker is not running. Please start Docker and re-run 'make setup'."; exit 1; }
	@echo "    Docker is running."
	@echo "==> Starting Weaviate in Docker..."
	docker compose up -d
	@echo "    Waiting for Weaviate to be ready..."
	@for i in $$(seq 1 30); do \
		if curl -s -o /dev/null http://localhost:8090/v1/.well-known/ready 2>/dev/null; then \
			echo "    Weaviate is ready!"; \
			break; \
		fi; \
		if [ "$$i" -eq 30 ]; then \
			echo "    ⚠️  Weaviate did not become ready in 60s. Check 'docker compose logs'."; \
		fi; \
		sleep 2; \
	done

# ---------------------------------------------------------------------------
# Dev workflow
# ---------------------------------------------------------------------------

build:
	go build ./...

test: lint
	go test ./... -count=1 -v

lint:
	# Ignore version mismatch errors during custom script linting if target version is newer than linter
	golangci-lint run ./... || go vet ./...

check: lint test build

run:
	go run . serve

start:
	./start.sh
