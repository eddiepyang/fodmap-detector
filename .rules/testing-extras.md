# Test Doubles and Helpers

Source: Google Go Style Guide — `best-practices#test-double-and-helper-packages`, `best-practices#shadowing`, `best-practices#package-size`.

## Test double packages
- Name the package by appending `test` to the production package: `creditcard` → `creditcardtest`
- Mark the Bazel `go_library` as `testonly = True`
- Single type doubled: name the double concisely (`Stub`, not `StubService`)
- Multiple behaviors: name by emulated behavior (`AlwaysCharges`, `AlwaysDeclines`)
- Multiple types doubled: include the type (`StubService`, `StubStoredValue`)

## Local variables in tests
- Prefix the double's variable name when it sits alongside production types (`spyCC`, not `cc`)

## Shadowing
- Short-declaring a variable in a nested scope *shadows* the outer one — the outer is unchanged after the block
- Short-declaring in the same scope *stomps* (assignment); safe when the old value is no longer needed
- Beware stomping a package name (`url := ...` blocks `net/url`)
- Intentional shadowing is fine if it improves clarity; otherwise pick a new name

## Package size
- Users see a package's godoc on one page; group tightly-coupled related types together
- If clients must import two packages to use either meaningfully, merge them
- Don't put the whole project in one package either; conceptually distinct code gets its own small package
- No "one type per file" rule; files should be focused and findable, neither huge nor tiny