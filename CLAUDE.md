# CLAUDE.md

Project-level rules for this codebase.

## Go Style

- Follow the [Google Go Style Guide](https://google.github.io/styleguide/go/)
- No `os.Exit` or `panic` in library code — only CLI entry points may call `os.Exit`
- No logging inside functions that also return errors — let callers decide whether to log
- Initialism casing: `ID` not `Id`, `URL` not `Url`
- Remove dead code — no commented-out code, TODO stubs, or unused types
- Use `errors.Is` for sentinel errors (`errors.Is(err, io.EOF)` not `err == io.EOF`)
- Import grouping: stdlib, then project packages, then third-party, each separated by a blank line
- Doc comments on all exported identifiers — must begin with the exported name

## UI & API Mapping
- **Main Chat Logic**: `chat/chat.go` (Server) -> `POST /chat`
- **Conversation CRUD**: `server/handlers.go` -> `/conversations` (List, Create, Get, Delete)
- **Restaurant Search**: `server/handlers.go` -> used by `NewChatWorkflow.tsx` for initial selection
- **Authentication**: `server/middleware.go` (bearerAuth middleware)

## Architecture

- **`chat` package** (`chat/chat.go`) is the shared core for FODMAP/allergen chat — CLI and server are thin wrappers
- **Chat endpoint** (`POST /chat/{query...}`) requires bearer token auth, per-IP rate limiting, concurrency limiter
- **Middleware** (`server/middleware.go`): `bearerAuth` → `rateLimitMiddleware` → `concurrencyLimiter` via `chain()`; auth runs outermost so unauthenticated requests don't consume rate limit tokens
- **`GeminiChatFactory`** function type enables test stubbing without mocking `genai.Chat`
- **Test constructors**: `server.NewServerWithChat()` for integration tests, `noopGeminiFactory` for tests that don't reach Gemini

## Git Workflow

- Always pull the latest changes from `main` (`git pull origin main`) before starting a new task or creating a branch.

## Testing

- Always use Test-Driven Development (TDD). Write tests before writing the implementation.
- Running the golang linter is not optional. It is now bound to the `make test` pipeline.
- **Always run `make test`** instead of relying on raw `go test ./...` in isolation, this guarantees local `golangci-lint` passes before regressions are accepted. You should enforce this local rule.
- Do not mock — use stub types that implement interfaces (see `integration/handlers_test.go` for the pattern)
- Always write tests for new functionality — every new exported function, HTTP handler, or CLI helper must have accompanying tests in a `_test.go` file in the same package
- For functions that call external HTTP endpoints, extract the base URL to a package-level `var` (e.g. `var offBaseURL = "https://..."`) so tests can redirect to an `httptest.NewServer` without mocking

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

## Error handling

- CLI commands use `RunE` (not `Run`) and return `fmt.Errorf("context: %w", err)` — no `slog.Error` + `os.Exit` in command handlers
- Root command sets `SilenceErrors: true` and `SilenceUsage: true`; `Execute()` in `root.go` handles printing
- Use `_ = x.Close()` when closing in an error cleanup path (primary error already captured)
- `defer x.Close()` is fine for deferred cleanup — errcheck is configured to ignore `.Close` on defers (see `.golangci.yml`)

## Resource lifecycle

- Functions that open files for streaming (e.g. `GetArchive`) return `(result, io.Closer, error)` — the caller owns the lifecycle and must `defer closer.Close()`
- Accept interfaces, return structs: constructor parameters should use interface types (`io.WriteCloser`, `io.Reader`) so callers can pass any implementation

## Logging

- Use `slog` throughout — not `log` or `fmt.Println`
- Structured key/value pairs: `slog.Error("msg", "key", value)` — never bare string concatenation

## Static Assets

- Always use `//go:embed` for static text files, prompts, and templates instead of reading from disk with `os.ReadFile`. This bundles assets directly into the Go binary and guarantees they are present at deployment.
