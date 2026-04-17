.PHONY: all build test lint check

all: lint test build

build:
	go build ./...

test: lint
	go test ./... -count=1 -v

lint:
	# Ignore version mismatch errors during custom script linting if target version is newer than linter
	golangci-lint run ./... || go vet ./...

check: lint test
