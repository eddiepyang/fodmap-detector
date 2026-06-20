# Naming

Source: Google Go Style Guide — `decisions#naming`.

## Package names
- Concise, lowercase only, no underscores (e.g. `tabwriter`, not `tabWriter` or `tab_writer`)
- Avoid uninformative names: `util`, `common`, `helper`, `model` — they invite import renaming
- Avoid names likely shadowed by common local variables (prefer `usercount` over `count`)
- Generated-only packages may use underscores: `_test` suffix for black-box/integration tests

## Receiver names
- Short (one or two letters), an abbreviation of the type
- Consistent across every receiver for that type
- Never `_`; omit the name if unused

## Constant names
- MixedCaps like all other names; never `K` prefix or ALL_CAPS
- Name by role, not value (`MaxPacketSize`, not `Twelve`)

## Getters
- No `Get`/`get` prefix unless the concept is literally an HTTP GET
- Prefer the noun: `Counts` over `GetCounts`; use `Compute`/`Fetch` for expensive calls

## Variable names
- Length proportional to scope, inversely proportional to use count
- Reflect contents and use in context, not the origin field/struct name
- Omit type-like words: `users` not `userSlice`, `userCount` not `numUsers`
- Omit words clear from surrounding context

## Repetition
- Don't repeat the package name in exported symbols (`widget.New`, not `widget.NewWidget`)
- Don't repeat the receiver type in method names
- Don't repeat parameter variable names in the function name
- Don't repeat return type info in the name