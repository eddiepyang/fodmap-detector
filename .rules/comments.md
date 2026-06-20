# Comments and Documentation

Source: Google Go Style Guide — `decisions#commentary`, `best-practices#documentation-conventions`.

## Line length
- No fixed column limit for comments; wrap long lines for readability on narrow screens
- 80–100 columns is a common choice; be consistent within a file
- Don't break long URLs or literals if breaking harms readability

## Doc comments
- All top-level exported names need doc comments; also unexported types/funcs with non-obvious behavior
- Full sentences beginning with the name being described (an article may precede it)
- Written for users of the package (surfaced in Godoc / IDEs)
- Apply to the following symbol, or group of fields in a struct
- For unexported code with doc comments, follow the same convention as exported (eases later export)

## Sentences
- Complete sentences: capitalize and punctuate like standard English
- Sentence fragments (e.g. end-of-line field comments): no requirement

## Examples
- Provide runnable `Example*` functions in `_test.go` files; they appear in Godoc
- If a runnable example isn't feasible, put code in a comment

## Named result parameters
- Add names only when they aid Godoc clarity (e.g. two same-typed returns, or required caller action like `cancel`)
- Don't name results just to enable naked returns or avoid declaring a local
- Never name results redundantly with the type (`(node *Node)`)

## Package comments
- Single package comment per package, immediately above the `package` clause (no blank line)
- `main` packages use the binary/go_binary name as the subject
- If no obvious primary file or comment is very long, put it in `doc.go` with only the comment and package clause
- Maintainer-only file-level comments go after imports, not above the package clause

## Documentation conventions
- Document error-prone or non-obvious parameters/fields, not every one
- Don't restate implied context semantics (e.g. "returns ctx.Err() on cancellation")
- Document non-obvious concurrency semantics (e.g. LRU cache `Lookup` is not safe for concurrent use)
- Document error contracts: what can be returned and when callers should use `errors.Is`/`errors.As`