# Contexts

Source: Google Go Style Guide — `best-practices#contexts`, `best-practices#documentation-conventions-contexts`.

- Pass `context.Context` as the first argument of functions that can be cancelled or scoped
- Do not add a `context.Context` field to a struct; add a `ctx` parameter to each method that needs it
- Exception: method signatures that must match an interface in the stdlib or a third-party library — very rare
- Do not create custom context types or use interfaces other than `context.Context` in signatures — no exceptions
- Cancellation of `ctx` is implied to interrupt the function; if it returns an error, conventionally `ctx.Err()`. Don't restate this in docs
- Document only when behavior differs: returns a non-`ctx.Err()` error on cancellation, has other interrupt mechanisms, or has special lifetime/lineage/value expectations