# Testing

The project maintains a high test coverage standard (minimum **70%** for non-CLI packages) to ensure reliability.

## Running Tests

```bash
# Run lint, tests, and build (the standard CI gate)
make check

# Run tests only
make test

# Run tests with coverage report
go test -coverprofile=coverage.out ./...

# View coverage summary by function
go tool cover -func=coverage.out
```

`make check` runs `golangci-lint`, then `go test`, then `go build` — this is the same gate that CI enforces. Always run `make check` before committing.

## Coverage Threshold (CI)

The GitHub Actions pipeline enforces a 70% coverage threshold. If total coverage (excluding the `cli/` package) drops below this level, the build will fail.

To run the same check locally:
```bash
go test ./... -coverprofile=coverage.out
grep -v "fodmap/cli" coverage.out > coverage_filtered.out
go tool cover -func=coverage_filtered.out | grep total:
```

## Testing Conventions

- **No mocking frameworks.** Tests use hand-written stub types that implement interfaces (see `integration/handlers_test.go` for the pattern). Do not use `gomock`, `mockery`, or similar.
- **HTTP tests use `httptest.Server`** to mock external APIs like Weaviate, Pinecone, and Gemini. This provides fast, deterministic unit tests without requiring real infrastructure.
- **Tests for new exported functions, HTTP handlers, or CLI helpers** must have corresponding `_test.go` files in the same package.
- **For functions calling external HTTP endpoints**, extract the base URL to a package-level `var` so tests can redirect to an `httptest.NewServer`.

## Integration Tests

Integration tests live in `integration/` and test full HTTP handler flows against a real Postgres database. These require `POSTGRES_DSN` to be set and use `server.NewServerWithChat()` to inject a no-op Gemini factory.

```bash
# Run integration tests (requires Postgres)
POSTGRES_DSN="postgres://fodmap:fodmap@localhost:5432/fodmap?sslmode=disable" go test ./integration/...
```