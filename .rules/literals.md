# Composite Literals

Source: Google Go Style Guide — `decisions#literal-formatting`.

## Field names
- Always specify field names for types defined outside the current package
- Field names optional for package-local types; use them when clearer, and almost always for large structs

## Matching braces
- Closing brace on a line with the same indentation as the opening brace
- End multi-line element lines with a comma; put `}` on its own line
- Never share a line between a value and the closing brace of a multi-line literal

## Cuddled braces
- Cuddling (`{{...}}`) allowed only when indentation matches AND inner values are also literals/proto builders (not variables/expressions)

## Repeated type names
- Omit repeated type names in slice/map literals: `[]*Type{{A: 1}, {A: 2}}`, not `[]*Type{&Type{A: 1}, ...}`
- `gofmt -s` does this automatically

## Zero-value fields
- Omittable when clarity isn't lost; well-designed APIs lean on zero-value construction
- In table-driven tests, omit zero-valued unrelated fields; specify fields that matter for the case