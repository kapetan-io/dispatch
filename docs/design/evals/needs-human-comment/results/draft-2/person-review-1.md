## In plain English

A reviewer on the ENG-290 pull request asked that the launcher also skip tickets with no repo label at all, because those would start in the wrong worktree. I checked, and an unlabeled ticket never gets that far, since the dispatcher's filter drops it before the launcher sees it. I replied on the PR with the evidence. I'm moving the ticket to Needs Human because the claim came from a person, so you decide whether my reply settles it.

The input in question is a ticket whose label list is empty. This test feeds one to the dispatcher and asserts it is never launched.

```
$ go test ./... -run UnlabeledTicketIsNeverLaunched
ok  	github.com/kapetan-io/dispatch	0.31s
```

## Details

**State:** The PR is open and the reviewer's comment is unresolved. I changed no code in response to it.

**Reviewer's claim:** "This should also skip tickets with no repo label at all. Otherwise they get launched into the wrong worktree."

**Evidence:** The dispatcher's filter removes unlabeled tickets, so the launcher only receives tickets that carry a repo label. The test above passes against the current branch. ENG-290 covers the case where the label is present but unknown, and the launcher skip handles exactly that.

**Decision needed:** If you agree the filter already covers this, tell me and I'll resolve the thread. If the reviewer wants a second skip in the launcher as a guard against the filter changing later, I think that belongs in its own ticket, because it widens ENG-290 beyond its title. Which do you want?
