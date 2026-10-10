Tickets with no repo label already never reach the launcher, so they can't be launched into the wrong worktree. The dispatcher's filter drops them before the launcher runs.

I checked this with a test that feeds the dispatcher an unlabeled ticket and asserts it is never launched:

```
$ go test ./... -run UnlabeledTicketIsNeverLaunched
ok  	github.com/kapetan-io/dispatch	0.31s
```

So the skip this PR adds only matters for tickets that carry a repo label the launcher doesn't recognize. Adding a second check for the no-label case would duplicate the filter and leave two places to keep in sync.

I'm not changing the code. If you've seen an unlabeled ticket get launched, send me the ticket or the log and I'll trace that path, since it would mean the filter has a gap my test doesn't cover. Otherwise, do you agree the filter is the right place to own this?

Moving this to Needs Human until you've had a look.
