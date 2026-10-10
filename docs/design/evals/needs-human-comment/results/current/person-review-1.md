Tickets with no repo label never reach the launcher, so they can't land in the wrong worktree. The dispatcher's filter drops them first and escalates them. I fed it an unlabeled ticket in a test, and it escalated the ticket instead of launching it.

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

A second skip in the launcher would be dead code, because no unlabeled ticket can get that far. I haven't changed the PR.

If you've seen an unlabeled ticket get launched, send me the ticket and I'll trace how it got past the filter. Otherwise, are you fine leaving this to the filter? I'm moving the ticket to Needs Human until you reply.
