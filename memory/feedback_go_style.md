---
name: feedback_go_style
description: User wants Google Go Style Guide applied to this codebase — specific priorities and patterns to follow
type: feedback
---

The user wants the codebase to follow the [Google Go Style Guide](https://google.github.io/styleguide/go/).

## Key priorities (applied 2026-03-15)

- **No `os.Exit` or `panic` in library code** — only `main` (CLI entry points) should call `os.Exit`. Library functions must return errors for callers to handle.
- **No logging inside functions that also return errors** — style guide says "let callers decide whether to log". Remove `slog.Error` calls that precede a `return ..., err`.
- **Initialism casing** — Go initialisms must be uniformly capitalized: `ID` not `Id`, `URL` not `Url`, etc.
- **Dead code removal** — remove commented-out code, TODO stubs, and unused types/functions rather than leaving them in the codebase.
- **`errors.Is` for sentinel errors** — use `errors.Is(err, io.EOF)` instead of `err == io.EOF`.
- **Import grouping** — stdlib, then project packages, then third-party, each separated by a blank line.
- **Doc comments on all exported identifiers** — comments must begin with the exported name and use complete sentences.
