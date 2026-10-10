## In plain English
A reviewer asked me to make the launcher skip tickets that have no repo label, so they don't start in the wrong worktree. I checked, and that case is already covered. The dispatcher drops an unlabeled ticket before the launcher ever sees it, so I haven't changed the code. I need you or the reviewer to confirm that, or to tell me about a case where an unlabeled ticket did get launched.

```
Ticket labels:  []            (no repo label)
Dispatcher:     drops it before the launcher, so no worktree is chosen
```

## Details
**State:** ENG-290 is In Review with its PR open. I'm moving it to Needs Human because the review claim came from a person and I couldn't confirm it.

**Review claim:** "This should also skip tickets with no repo label at all. Otherwise they get launched into the wrong worktree."

**Evidence:** I wrote a test that feeds the dispatcher a ticket with no repo label. It shows the ticket never reaches the launcher, because the dispatcher's filter drops it first. The test passes.

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

**What I did:** I added no skip for unlabeled tickets in the launcher. A second check there would duplicate the dispatcher's filter, and the two could drift apart.

**Decision I need:** Is the dispatcher's filter enough for the reviewer? If they saw an unlabeled ticket launch, please send the ticket or the command so I can reproduce it. If they want the launcher to check as well, say so and I'll add it with a test.
