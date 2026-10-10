## In plain English

A teammate reviewing the pull request for this ticket says tickets with no repo label should also be skipped, or they get launched into the wrong worktree. I checked, and an unlabeled ticket never gets that far. The dispatcher's filter drops it before the launcher (the part that starts a worker in a worktree) sees it. A person made the claim and I can't confirm it, so I need you or the reviewer to say whether my evidence settles it.

Here is the kind of ticket the reviewer is worried about (illustrative, not a real ticket).

```
title:  Fix retry backoff
labels: [bug]        <- no repo label
```

## Details

**Reviewer's claim.** "This should also skip tickets with no repo label at all. Otherwise they get launched into the wrong worktree."

**What I checked.** I wrote a test that feeds the dispatcher a ticket with no repo label and watches what happens to it. The test passes.

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

Because the filter drops the ticket first, a second skip rule in the launcher would never run. This ticket's change, skipping tickets whose repo label is unknown, covers the case the filter doesn't, which is a label that is present but matches no repo.

**What I did not change.** I made no code change for this comment, since I found no path where an unlabeled ticket reaches the launcher.

**Decision needed.** Either the reviewer confirms the test covers the case they meant, or they point me to a path that bypasses the filter and I fix it. I'm holding the PR here until one of you replies.
