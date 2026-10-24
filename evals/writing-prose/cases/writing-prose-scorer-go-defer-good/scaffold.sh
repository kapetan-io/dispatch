#!/bin/bash
set -e
cat > canned.md <<'CANNED'
In Go, `defer` schedules a call to run when the surrounding function returns, which makes it the way to release a resource on every path out of the function. It does not run at the end of the enclosing block.

The arguments of the deferred call are evaluated when the `defer` statement runs, not when the call does, so `defer fmt.Println(i)` prints the value `i` had at that point. Deferred calls run last in, first out, so a file opened first is closed last. They also run if the function panics. A deferred function literal can read and set the function's named results, which lets it replace a nil error return with the error from `Close`.

Because nothing runs until the function returns, a `defer f.Close()` inside a loop keeps every file open until the loop's function ends. A loop body that opens one file per iteration belongs in its own function, which releases each file when it returns.
CANNED
