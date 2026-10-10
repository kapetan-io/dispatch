## In plain English
A reviewer asked that the change in this PR also skip tickets that have no repo label, because they worried those tickets would be launched into the wrong worktree. I checked, and that can't happen. The dispatcher drops an unlabeled ticket before the launcher ever sees it, so adding the skip to the launcher would be dead code. I'm leaving the code as it is and need the reviewer to confirm or point me at a case I missed.

```
Ticket labels: Bug
(no repo label)
```

## Details
**State:** PR is open and I'm moving the ticket to Needs Human. I haven't changed anything in response to the review comment.

**Evidence:** I wrote a test that feeds the dispatcher a ticket with no repo label and checks what happens to it. The ticket is escalated instead of being placed in a repo, and it never reaches the launcher.

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

**Decision needed:** If you have a path where an unlabeled ticket still gets launched, such as a different entry point or a label that gets removed after the dispatcher's filter runs, tell me how it gets there and I'll add a failing test and the skip. Otherwise I'd resolve the review thread as is. Which one is it?
