## In plain English

A reviewer asked me to make the launcher skip tickets that have no repo label, because they think those tickets would start in the wrong worktree. I checked, and that path doesn't exist. The dispatcher drops an unlabeled ticket before the launcher ever sees it, so the PR as written already covers it.

A ticket like this never reaches the launcher:

```
Ticket ENG-999
Labels: (none)
```

I'm not changing the code, since I can't reproduce the problem. I'm moving this to Needs Human so you or the reviewer can say whether the trace settles it. If the reviewer has a case where an unlabeled ticket does get launched, I'd like to see it.

## Details

**Reviewer's claim**
"This should also skip tickets with no repo label at all. Otherwise they get launched into the wrong worktree."

**Evidence**
I fed the dispatcher a ticket with no repo label in a test. It was filtered out before the launcher, and the test passes.

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

**What I did**
- Replied on the PR with this evidence.
- Made no code change.
- Moved the ticket to Needs Human because the claim came from a person and I couldn't confirm it.

**Decision for you**
Is the test enough to close the review thread, or does the reviewer have a case I haven't covered? I'd close it.
