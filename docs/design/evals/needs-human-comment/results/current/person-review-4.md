Moving this to Needs Human. I can't reproduce the wrong-worktree launch you describe, and I'd like you to check my read before I change anything.

A ticket with no repo label never reaches the launcher. The dispatcher's filter drops it first, so the launcher only sees tickets that have a label. I fed the dispatcher an unlabeled ticket and asserted nothing gets launched:

```
$ go test ./... -run UnlabeledTicketIsNeverLaunched
ok  	github.com/kapetan-io/dispatch	0.31s
```

The skip in this PR covers the other case, a ticket whose label is present but names no known repo. That one passes the filter, so it needs its own check in the launcher.

Adding a second skip for unlabeled tickets would be dead code. It also hides the dispatcher filter as the real place that rule lives.

If you saw an unlabeled ticket launch somewhere, send me the ticket or the log. That would mean the filter has a gap my test doesn't cover, and I'd fix it in the filter. Otherwise, can you confirm the filter is the right home for this rule so I can resolve the thread?
