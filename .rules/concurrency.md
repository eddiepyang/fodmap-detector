# Concurrency

Source: Google Go Style Guide — `best-practices#goroutine-lifetimes`.

## Goroutine lifetimes
- Never start a goroutine without knowing how it will stop
- Goroutines leaked at function return cause subtle bugs: deadlocks, stale references, non-deterministic shutdown
- Make the exit path explicit: a channel close, a context cancellation, or a bounded loop condition

See also: `.rules/channels.md` for channel consumption patterns.