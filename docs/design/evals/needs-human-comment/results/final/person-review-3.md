## In plain English

A reviewer on the PR for ENG-290 thinks a ticket with no repo label at all could get launched into the wrong worktree, so the launcher should skip those tickets too. I checked and I don't think that can happen. The dispatcher drops an unlabeled ticket before the launcher ever sees it, so the PR doesn't need the extra skip.

An example of the input in question:

```
ENG-999  "Retry the webhook on timeout"
labels:  bug
repo:    (none)
```

The dispatcher can't place this ticket in a repo, so it escalates it instead of passing it on. It never gets a worktree.

I need you to settle this with the reviewer, because I'm not going to change their mind by arguing on the PR. I'd leave the code as it is. A second check inside the launcher would be dead code that no surface test can reach. If you'd still like it as a safety net, say so and I'll add it.

## Details

**State.** The PR is open and the ticket is moving to Needs Human. The reviewer's claim is unconfirmed, and they are a person rather than a bot.

**Evidence.** I wrote a test that feeds the dispatcher a ticket with no repo label. It passes, and the ticket is escalated rather than launched.

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

**Decision for you.** Keep the PR as it is, or add a second no-label skip in the launcher. I recommend keeping it as it is. I haven't changed any code in response to the comment.
