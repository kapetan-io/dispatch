## In plain English

A reviewer asked that tickets with no repo label be skipped too, because they worry those tickets get launched into the wrong worktree. I couldn't reproduce that. An unlabeled ticket like this one never reaches the launcher.

```
ENG-999  "Fix flaky retry"
labels:  bug, backend        (no repo label)
```

The dispatcher's filter drops it before the launcher sees it, so no worktree is chosen for it. The change in this PR only covers tickets whose repo label is present but unknown. I'm moving the ticket to Needs Human because the claim came from a person, and I don't want to dismiss it on my own say-so.

## Details

**Status:** the PR is open and I haven't changed the code in response to this comment.

**Evidence:** `TestTickEscalatesTicketItCannotPlaceInARepo` feeds the dispatcher an unlabeled ticket and checks that it never reaches the launcher.

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

**Decision for the reviewer:** I can add a second skip for unlabeled tickets in the launcher. It would never fire today, and it would make the launcher depend on a filter rule that lives elsewhere. I'd leave it out. If you've seen an unlabeled ticket get launched, send me its ID and I'll trace that path. Otherwise, please resolve the thread and I'll move the ticket back to In Review.
