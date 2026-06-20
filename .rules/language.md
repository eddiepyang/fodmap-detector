# Language Features

Source: Google Go Style Guide — `decisions#generics`, `decisions#any`, `decisions#unnecessary-pointers`, `decisions#equality`, `best-practices#time-formats`.

## Generics
- Allowed where they fulfill a real business requirement
- Beware premature use: slices/maps/interfaces often work as well without the complexity
- Don't use generics just because an algorithm is type-agnostic and only one type is instantiated in practice
- If several types share a useful unifying interface, prefer the interface over generics
- Document exported generic APIs and include runnable examples

## `any` vs `interface{}`
- Prefer `any` in new code (Go 1.18+ alias for `interface{}`)

## Unnecessary pointers
- Don't pass pointers to save a few bytes when the function only reads `*x`
- Common offenders: `*string`, `*io.Reader` — the value is fixed-size; pass directly
- Does not apply to large structs or protobuf messages (use pointers; satisfies `proto.Message`)

## Equality
- `==` works for scalars and some structs/interfaces; pointers compare by identity
- For structs needing semantic equality or containing non-comparable fields (e.g. `io.Reader`), use `cmp.Equal`/`cmp.Diff` with `cmpopts`
- Do not use `pretty` for protobuf comparison — it misses nil-vs-empty slices and interface differences and reads unexported fields

## Time formats
- Use `time.Time` for times; avoid string math on times
- Use the package's reference layout (`time.Parse(time.RFC3339, ...)`) not hand-rolled formats