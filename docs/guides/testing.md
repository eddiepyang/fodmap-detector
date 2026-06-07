# Testing

## Testing

The project maintains a high test coverage standard (minimum **70%** for non-CLI packages) to ensure reliability.

### Running Tests

```bash
# Run all tests
go test ./...

# Run tests and generate coverage report
go test -coverprofile=coverage.out ./...

# View coverage summary by function
go tool cover -func=coverage.out
```

### Coverage Threshold (CI)

The GitHub Actions pipeline is configured to enforce a 70% coverage threshold. If total coverage (excluding the `cli/` package) drops below this level, the build will fail. 

To run the same check locally:
```bash
go test ./... -coverprofile=coverage.out
grep -v "fodmap/cli" coverage.out > coverage_filtered.out
go tool cover -func=coverage_filtered.out | grep total:
```

### Mocking

Many tests (especially in `chat/` and `search/`) use `httptest.Server` to mock external APIs like Gemini and Weaviate. This allows for fast, deterministic unit testing without requiring real API keys or running infrastructure.

---

