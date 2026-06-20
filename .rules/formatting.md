# Formatting

Source: Google Go Style Guide — `decisions#func-formatting`, `decisions#conditional-formatting`, `decisions#indentation-confusion`.

## Function signatures
- Keep function/method declarations on a single line
- Don't break argument lists solely on line length — it creates indentation confusion with the body
- Shorten call sites by extracting locals rather than wrapping mid-call
- Group semantically related arguments when a call genuinely must wrap

## Inline argument comments
- Avoid inline comments on specific arguments; use an option struct or expand the doc comment

## Conditionals and loops
- Don't line-break `if` conditions; extract boolean operands into named locals instead
- Avoid line breaks that align wrapped code with an indented block; add a separating space if unavoidable

## String literals
- Don't break long string literals for line length
- May break after the format string and continue with arguments on subsequent lines, grouped semantically