# Logging

Source: Google Go Style Guide — `best-practices#logging-errors`, project convention.

- Use `slog` for all diagnostic/log messages in server and library code — not `log` or `fmt.Println`
- Structured key/value pairs: `slog.Error("msg", "key", value)` — never bare string concatenation
- CLI exception: `fmt.Printf`/`fmt.Println` is permitted for **direct user output** in `cli` commands (the thing the user invoked the command to see — e.g. chat responses, scrape summaries, migrate results). slog remains required for diagnostic messages (warnings, errors, progress that isn't the command's product output).
- Use the `"error"` key (not `"err"`) for error values in slog calls, for codebase consistency