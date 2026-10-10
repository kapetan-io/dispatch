## In plain English

ENG-290 makes the launcher skip tickets whose repo label names a repo we don't know. A reviewer on the pull request asked for the same skip on tickets with no repo label at all, because they worry those get launched into the wrong worktree. I checked, and that can't happen today, since the dispatcher's filter drops unlabeled tickets before the launcher ever sees them. The reviewer is a person, so I haven't resolved their thread and need you to confirm my reading or tell me what they saw.

Example of the input in question (illustrative, a ticket carrying only a non-repo label):

```
labels: ["Bug"]
```

Fed to the dispatcher, this ticket produces no launch.

## Details

**Review comment:** "This should also skip tickets with no repo label at all. Otherwise they get launched into the wrong worktree."

**Evidence:** a test that gives the dispatcher an unlabeled ticket and asserts the launcher is never called.

```
$ go test ./... -run UnlabeledTicketIsNeverLaunched
ok  	github.com/kapetan-io/dispatch	0.31s
```

**Attempts:** I replied on the PR with this evidence and left the thread open. I made no code change, because a second skip for unlabeled tickets would never run. It would also leave two places that decide the same thing.

**Decision needed:** either confirm the dispatcher's filter is the intended place to drop unlabeled tickets, so I can resolve the thread, or tell me the case the reviewer has in mind. If they saw an unlabeled ticket launch, that is a bug the test doesn't cover, and I'd want to reproduce it first.

The ticket is in Needs Human until you answer.
