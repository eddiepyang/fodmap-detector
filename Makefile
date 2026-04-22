.PHONY: all build test lint check setup-llama

all: lint test build

setup-llama:
	@if [ ! -d "llama-go" ]; then \
		echo "Cloning llama-go..."; \
		git clone --recurse-submodules https://github.com/tcpipuk/llama-go.git; \
		echo "Applying CGO compiler flags patch..."; \
		cd llama-go && patch -p1 < ../llama-go.patch; \
	fi
	@echo "Building llama-go bindings..."
	@cd llama-go && make libbinding.a CMAKE_ARGS="-DBUILD_SHARED_LIBS=OFF"

build:
	go build ./...

test: lint
	go test ./... -count=1 -v

lint:
	# Ignore version mismatch errors during custom script linting if target version is newer than linter
	golangci-lint run ./... || go vet ./...

check: lint test
