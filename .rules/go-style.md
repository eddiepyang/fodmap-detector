Follow the Google Go Style Guide (https://google.github.io/styleguide/go/).

Naming:
- Initialism casing: `ID` not `Id`, `URL` not `Url`

Error handling:
- No `os.Exit` or `panic` in library code — only CLI entry points may call `os.Exit`
- No logging inside functions that also return errors — let callers decide whether to log
- Use `errors.Is` for sentinel errors (`errors.Is(err, io.EOF)` not `err == io.EOF`)
- CLI commands use `RunE` (not `Run`) and return `fmt.Errorf("context: %w", err)` — no `slog.Error` + `os.Exit` in command handlers
- Root command sets `SilenceErrors: true` and `SilenceUsage: true`; `Execute()` in `root.go` handles printing
- Use `_ = x.Close()` when closing in an error cleanup path (primary error already captured)
- `defer x.Close()` is fine for deferred cleanup — errcheck is configured to ignore `.Close` on defers

Dead code:
- Remove dead code — no commented-out code, TODO stubs, or unused types

Import grouping:
- stdlib, then project packages, then third-party — each separated by a blank line

Doc comments:
- Doc comments on all exported identifiers — must begin with the exported name