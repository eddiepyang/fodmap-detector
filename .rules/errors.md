# Errors

Source: Google Go Style Guide — `decisions#errors`, `best-practices#error-handling`.

## Returning errors
- `error` is the last result parameter
- A `nil` error signals success; non-error return values are otherwise unspecified (usually zero values)
- Exported functions return the `error` interface, never a concrete error type (avoid nil-in-interface bugs)
- Functions taking `context.Context` should usually return `error`

## Error strings
- Lowercase, no trailing punctuation (they're usually embedded in other context)
- Full displayed messages (logs, test failures, UI) are capitalized

## Handling
- Never discard with `_` without a justifying comment (e.g. `(*bytes.Buffer).Write` is documented never to fail)
- Handle, return, or (rarely) `log.Fatal`/`panic`

## In-band errors
- Don't return -1/nil/"" to signal failure; return an extra `error` or `ok bool` as the final value
- Prevents bugs like `Parse(Lookup(missingKey))` attributing the wrong failure

## Indent error flow
- Handle errors before the normal path; don't wrap the normal path in `else`
- `if err != nil { return }; normal code` — not `if err != nil { ... } else { normal }`

## Wrapping (`%w` vs `%v`)
- `%v`: simple annotation, logging, or translating at system boundaries (RPC/IPC/storage) — drops structured info
- `%w`: when callers should be able to `errors.Is`/`errors.As` the underlying error; forms an unwrap chain
- Prefer `%w` at the *end* of the string: `"couldn't find remote file: %w"`
- Exception — sentinel errors go at the *start*: `"%w: invalid character in header: %v"` so the category is prominent

## Adding context
- Add only non-redundant info (the underlying error usually already has paths/ops)
- Don't annotate with `"failed: %v"` — just return `err`

## Structure
- Give errors structure (sentinel values, typed errors) when callers must distinguish conditions
- Check with `errors.Is`/`errors.As`; never string-match on `.Error()`

## Terminal HTTP handler writes
- `_ = json.NewEncoder(w).Encode(...)` / `_ = w.Write(...)` / `_, _ = fmt.Fprintf(w, ...)` is permitted in HTTP handlers **once the response has begun** (status and headers written), with a one-line justifying comment. The error cannot be surfaced to the client after headers are flushed and the connection may already be gone.
- Before the first write, errors must be handled (e.g. via `respondError`).

## Logging errors
- If you return an error, usually don't log it too — let the caller decide
- `log.Error` is expensive (forces a flush); use sparingly and prefer actionable messages