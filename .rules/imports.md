# Imports

Source: Google Go Style Guide — `decisions#imports`, `best-practices#proto-imports`.

## Renaming
- Don't rename unless necessary (collision, uninformative name, generated proto)
- Local names follow package-name rules (lowercase, no underscores, no caps)
- Proto packages must be renamed to drop underscores and use `pb` (proto) or `grpc` suffix:
  `foopb "path/to/foo_service_go_proto"`
- On collision, prefer renaming the most local/project-specific import
- If renaming to free up a common var name (e.g. `url`), use the `pkg` suffix (`urlpkg`)

## Grouping (in order, blank-line separated)
1. Standard library
2. Other (project and vendored) packages
3. Protocol buffer imports
4. Side-effect imports (`_ "..."`)

## Blank imports (`import _`)
- Only in `main` packages or tests that require them
- Not in library packages, even indirect dependencies
- Exceptions: bypassing nogo checks; `embed` when using `//go:embed`

## Dot imports (`import .`)
- Never use in this codebase — obscures where symbols come from