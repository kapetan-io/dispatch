Write the explainer section for a Go language guide, for a developer about to use defer to release resources. The question it answers: When does a deferred call run in Go, for someone using defer to release a resource?

Facts, each checked against the Go specification:

- `defer` schedules a call to run when the surrounding function returns, not at the end of the enclosing block.
- The arguments of the deferred call are evaluated when the `defer` statement runs, not when the call runs.
- For a method call, the receiver is evaluated when the `defer` statement runs too.
- Deferred calls run last in, first out.
- Deferred calls also run when the function panics.
- A deferred function literal can read and set the function's named results.
- In a loop each iteration adds a deferred call, and none run until the function returns, so `defer f.Close()` in a loop keeps every file open until then.
- A deferred call of a nil function value panics when the call runs, not at the `defer` statement.
- Since Go 1.14 the compiler inlines most defers at function exit, so a defer costs a few nanoseconds.

Write the final section to section.md.
