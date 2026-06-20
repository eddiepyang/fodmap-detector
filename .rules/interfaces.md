# Interfaces

Source: Google Go Style Guide — `decisions#interfaces` and `best-practices#interfaces`.

## Ownership and location

### Consumer owns the interface

Interfaces belong in the package that *uses* them, not the package that implements
them. The consumer defines only the methods it actually uses (GoTip #78: Minimal
Viable Interfaces — "the bigger the interface, the weaker the abstraction").

### Keep internal interfaces unexported

If an interface is only used inside a package to satisfy a specific logic flow,
keep it unexported. Exporting an interface commits you to maintaining that API
for external callers.

### Producer may export the interface only when

- **The interface is the product** — a common protocol many implementations must
  follow (e.g. `io.Writer`, `hash.Hash`, generated protobuf interfaces). For
  large systems, put the interface in a standalone implementation-free package
  so clients don't have to import the entire implementation world just to
  reference the contract.
- **Preventing interface bloat** — when numerous packages would otherwise each
  define an identical `type Authorizer interface`; the producer may export once.
- **Breaking a circular dependency** — the producer returns an interface so it
  does not need to import the consumer's package. Caution: this is often a
  signal of improperly structured packages; consolidating packages is usually
  preferred over introducing an interface to break a cycle.

## When to create

### Avoid interfaces until a real need exists

Don't create an interface before a real need exists. Focus on the required
behavior rather than abstract named patterns.

- **Don't confuse the concept with the keyword** — designing a "service" or
  "repository" doesn't mean you need `type Service interface`. Focus on the
  behavior and its concrete implementation first.
- **Don't wrap RPC clients in manual interfaces** for abstraction or testing.
  Reuse existing/generated interfaces; use real transports instead.
- **Don't export test doubles solely for testing** — prefer testing via the
  public API of the real implementation. Export an interface for a test double
  only when there is a material need to support substitution.

### When it does make sense to create an interface

1. **Multiple implementations** — two or more concrete types handled by the
   same logic (e.g. something that works with both `json.Encoder` and
   `gob.GobEncoder`).
2. **Decoupling packages** — to break circular dependencies between two
   packages. Caution: often a signal of improperly structured packages.
3. **Hiding complexity** — a concrete type has a massive API surface but a
   specific function only needs one or two methods.

## Signatures

### Accept interfaces, return concrete types

GoTip #49: functions should take interfaces as arguments but return concrete
types. Returning a concrete type lets callers use the full API of that
implementation, not just the subset defined in a pre-chosen interface. The
caller can still pass the concrete result into any function expecting an
interface.

### When returning an interface is the idiomatic choice

1. **Encapsulation** — limit the default API surface and guide caller behavior.
   The most common example is the `error` interface; almost never return a
   concrete error type like `*MyCustomError`. Another example: a
   `ThrottledReader` returned as `io.Reader` to keep callers off an internal
   `Refill` method.
   - Caution: before returning an interface to hide implementation, ask whether
     a user calling the extra methods would actually break system integrity or
     limit maintainability. Do not rotely encapsulate without reason.

2. **Polymorphic patterns** — command, chaining, factory, and strategy patterns
   where a function returns one of several concrete types chosen at runtime.
   Avoid forcing an interface if a single robust concrete type can handle the
   abstraction internally (e.g. `database/sql` exports a concrete `DB`).

3. **Breaking a circular dependency** — returning a concrete type would require
   importing a package that already imports the current package.

## Design

### Keep interfaces small

The larger the interface, the harder it is to implement and to write code that
takes advantage of it. Small interfaces are easier to compose into larger ones
if needed.

### Documentation

Treat every interface as the "user manual" for the abstraction. Depth of
documentation should be proportional to cognitive load, not method count.

- **Single-method interfaces** — documentation on the type itself is usually
  sufficient (e.g. `io.Writer`). Explain its contract, edge cases, and expected
  errors.
- **Multi-method interfaces** — each individual method requires its own
  documentation.
- **Unexported interfaces** — consider documenting them anyway. They are often
  the glue holding complex internal logic together, and because they are
  invisible to external users, they can easily become mystery code for future
  maintainers.