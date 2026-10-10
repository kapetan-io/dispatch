## In plain English

ENG-290 makes the dispatcher skip a ticket whose repo label names a repo it doesn't know. The reviewer asked for the same skip when a ticket has no repo label at all, because it could otherwise be launched into the wrong worktree.

That case can't happen today. The dispatcher drops an unlabeled ticket before the launcher is ever called, so a second skip in the launcher would never run. Here is the kind of ticket the reviewer is worried about.

```
ENG-999  "Fix flaky retry test"
labels:  (none)
```

I'm not changing the PR. Since you raised a concern I can't confirm, I want you to decide whether it stands.

## Details

**Claim:** tickets with no repo label get launched into the wrong worktree and need the same skip.

**Evidence:** I wrote a test that gives the dispatcher an unlabeled ticket. The dispatcher's filter drops it, and the launcher is never called. The test passes.

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

**What I did not check:** whether any caller reaches the launcher without going through the dispatcher's filter. I found none in this PR.

**Decision for you:** if you want a skip in the launcher anyway as a second guard, I'll add it with a test. Otherwise I'll reply on the PR with this evidence and leave the change as it is. I've moved the ticket to Needs Human until you pick.
