#!/bin/bash
set -e
cat > canned.md <<'CANNED'
In Go, `defer` schedules a call to run when the surrounding function returns, which makes it the way to release a resource on every path out of the function. It does not run at the end of the enclosing block.

The arguments of the deferred call are evaluated when the `defer` statement runs, not when the call does, so `defer fmt.Println(i)` prints the value `i` had at that point. For a method call, the receiver is evaluated when the `defer` statement runs too. Deferred calls run last in, first out, so a file opened first is closed last. They also run if the function panics. A deferred function literal can read and set the function's named results, which lets it replace a nil error return with the error from `Close`. [VERIFY: confirm that named results are visible after a recovered panic]

Because nothing runs until the function returns, a `defer f.Close()` inside a loop keeps every file open until the loop's function ends. A loop body that opens one file per iteration belongs in its own function, which releases each file when it returns. A deferred call of a nil function value panics when the call runs, not at the `defer` statement. Since Go 1.14 the compiler inlines most defers at function exit, so a defer costs a few nanoseconds. Before that release a defer cost far more, which is why older advice says to avoid it in hot paths, and that advice no longer applies to code built with a current compiler. A function with several defers and several return paths runs the same deferred calls on each path, in the same order.

Deferred calls run last in, first out. They run when the function panics. A deferred function literal can set named results. A defer in a loop waits for the function to return.
CANNED
