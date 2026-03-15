# CLAUDE.md

Project-level rules for this codebase.

## Testing

- Always run `go test ./...` after any code change
- Run `golangci-lint run ./...` when available — it mirrors the CI lint step (install: `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`)
- Do not mock — use stub types that implement interfaces (see `integration/handlers_test.go` for the pattern)

## Go channel patterns

- Use `for range chan` to consume channels; do not use `select` with a separate done channel
- Closing a channel signals completion — ranging over it drains all buffered items automatically
- Prefer buffered channels in producer/consumer pipelines: `make(chan T, N)`

## API / routing changes

- Show a plan and get approval before changing any HTTP route pattern or query parameter contract
- Route patterns use Go 1.22+ method+path syntax: `"GET /path/{param}"` or `"GET /path/{wildcard...}"`
- Query strings are for optional filters; required inputs belong in the path

## Go version upgrades

- Run `go fix ./...` after bumping the Go version — it applies automated modernizations
- Keep `go.mod` at a single `go X.Y.Z` line; no `toolchain` directive needed when the installed version matches
