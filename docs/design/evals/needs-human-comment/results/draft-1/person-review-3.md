## In plain English

ENG-290 makes the dispatcher skip tickets whose repo label names a repo it doesn't know. A teammate reviewing the PR says it should also skip tickets with no repo label at all, because otherwise they get launched into the wrong worktree. I checked, and that can't happen. The dispatcher's filter drops an unlabeled ticket before the launcher (the part that starts a worker) ever sees it. I replied on the PR with the evidence, and since the reviewer is a person, I'm leaving the call to you on whether that answers them.

Example of the input in question, a ticket carrying no repo label:

```
Ticket:  ENG-999 "Fix retry backoff"
Labels:  (none)
```

The test feeds the dispatcher this ticket and asserts the launcher is never called.

```
$ go test ./... -run UnlabeledTicketIsNeverLaunched
ok  	github.com/kapetan-io/dispatch	0.31s
```

## Details

**Review comment (from a person):** "This should also skip tickets with no repo label at all. Otherwise they get launched into the wrong worktree."

**Evidence:** the test above passes. It sends an unlabeled ticket through the dispatcher and shows no launch happens. I did not add a second skip to the launcher, since the filter already covers the case and a duplicate would hide where the rule lives.

**State:** PR is open and the reply is posted. The ticket moves to Needs Human because I only resolve unconfirmed claims from bots, not from people.

**Decision for you:** if the evidence answers the reviewer, resolve their comment and move the ticket back to In Review. If they have a different path in mind where an unlabeled ticket reaches the launcher, ask them for it and I'll write a failing test for that path.
