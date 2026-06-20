# CLAUDE.md

Project-level rules for this codebase.

## Rules

Codestyle and convention rules live in `.rules/` as one file per topic. Read the
relevant file(s) before implementing — do not rely on the Google Go Style Guide
URL inline; the canonical content is already extracted there.

- `.rules/go-style.md` — top-level: follow Google Go Style Guide, initialism
  casing, library `panic`/`os.Exit` ban, `errors.Is`, dead code, import grouping,
  doc comments, CLI `RunE`/`SilenceErrors`/`SilenceUsage`, `_ = x.Close()` in
  error cleanup, errcheck on defers
- `.rules/naming.md` — packages, receivers, constants, getters, variables, repetition
- `.rules/imports.md` — renaming, grouping, blank imports, dot imports, proto conventions
- `.rules/comments.md` — line length, doc comments, sentences, examples, named
  results, package comments, documentation conventions
- `.rules/errors.md` — returning, strings, handling, in-band errors, indent flow,
  `%w`/`%v` placement, sentinel placement, structure, logging
- `.rules/literals.md` — field names, matching/cuddled braces, repeated types, zero-value fields
- `.rules/formatting.md` — function signatures, conditionals, indentation confusion, string literals
- `.rules/language.md` — generics, `any` over `interface{}`, unnecessary pointers, equality, time formats
- `.rules/context.md` — first-arg, no struct field, no custom context types
- `.rules/concurrency.md` — goroutine lifetimes
- `.rules/channels.md` — `for range chan`, close-to-signal, buffered producer/consumer
- `.rules/interfaces.md` — ownership, when to create, signatures (accept interfaces /
  return concrete types), design
- `.rules/resources.md` — streaming returns `(result, io.Closer, error)`; constructor interface types
- `.rules/logging.md` — `slog` throughout, structured key/value
- `.rules/static-assets.md` — `//go:embed` for text/prompts/templates
- `.rules/sql.md` — SQL in `.sql` files, `//go:embed`, golang-migrate, River tables
- `.rules/api.md` — Go 1.22+ method+path routing, plan before changing contracts
- `.rules/testing.md` — TDD, `make check`, stubs not mocks, per-feature tests, external-HTTP base URL var
- `.rules/testing-extras.md` — test double naming, shadowing, package size
- `.rules/usage.md` — usage messages

When a rule is needed but missing from `.rules/`, add it there (one file per topic)
rather than inlining it in this file.

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

## Go version upgrades

- Run `go fix ./...` after bumping the Go version — it applies automated modernizations
- Keep `go.mod` at a single `go X.Y.Z` line; no `toolchain` directive needed when the installed version matches

## Documentation & Scripts

- Always keep the `start.sh` script in working condition. If you make architectural changes, add new services, or change startup flags, you must update `start.sh` to reflect those changes.
- Ensure `README.md` and any relevant files in the `docs/` folder are kept up-to-date with your changes. Always verify instructions and CLI flags remain accurate.

## AI Agent Rules

- Always use a web search tool to confirm and fact-check findings or technical assumptions (e.g., model capabilities, version support, API limits) before finalizing recommendations or reporting back to the user. Do not rely solely on internal knowledge.
- Always review implementation plans **and planning documents** for risks, edge cases, and gaps after the first iteration to ensure robustness. Every plan in `docs/plans/` must include a "Risks and Gaps" section before it is considered complete.
- Prefer the LSP server (gopls) first for symbol-level questions — finding references, definitions, implementations, and call hierarchy. Use `grep`/text search only as a fallback when the LSP is unavailable or for non-symbol matches (comments, strings, file discovery). The LSP resolves actual symbol bindings; `grep` matches text and can false-match.