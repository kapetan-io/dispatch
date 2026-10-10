Unlabeled tickets never reach the launcher, so they can't land in the wrong worktree. The dispatcher's filter drops them first, so a launcher check would never run. I didn't add one, because dead code there would suggest the filter isn't the guard.

To check, I fed the dispatcher a ticket with no repo label. The test is `TestTickEscalatesTicketItCannotPlaceInARepo`, and it passes:

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

I'm moving this to Needs Human because I can't settle it without you. I may be missing a path where an unlabeled ticket gets past the filter, for example a label removed after the dispatcher has already picked the ticket up. If you've seen that happen, tell me the sequence and I'll write a test for it and fix the code. If the filter is the guard you wanted, reply here and I'll resolve the thread so the PR can merge.
