## In plain English
A teammate reviewing the PR for ENG-290 asked me to also skip tickets that have no repo label, because they would launch into the wrong worktree. I checked, and that can't happen. The dispatcher (the process that picks tickets and starts workers) drops an unlabeled ticket before the launcher (the part that creates a worktree and starts the worker) ever sees it. Since the claim comes from a person and I can't confirm it, I've left the code alone and need you to decide.

The test below gives the dispatcher a ticket with no repo label and checks that it never reaches the launcher.

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

## Details
**Reviewer's claim:** "This should also skip tickets with no repo label at all. Otherwise they get launched into the wrong worktree."

**Evidence:** The test `TestTickEscalatesTicketItCannotPlaceInARepo` feeds the dispatcher an unlabeled ticket. It passes, so the dispatcher's filter drops that ticket before launch. This PR only changes what happens to a ticket whose repo label is present but unknown. The unlabeled path is already covered, so a second skip for it would be dead code.

**State:** The PR is open and its code is unchanged by this review comment. The ticket is in Needs Human because the claim is from a person.

**Decision needed:** I think we should leave the code as is and reply to the reviewer with the test above. If they still want a guard in the launcher itself as a second line of defense, that is a scope call and it's yours. Which do you want?
