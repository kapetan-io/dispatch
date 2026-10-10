## In plain English

ENG-290 makes the launcher skip tickets whose repo label names a repo we don't know. A teammate reviewing the PR asked for it to skip tickets with no repo label too, because otherwise they get launched into the wrong worktree. I checked, and that can't happen. The dispatcher drops unlabeled tickets before the launcher ever sees them, so I changed nothing. Because the reviewer is a person, I'm leaving it to you to settle whether my evidence answers them.

The review comment:

> This should also skip tickets with no repo label at all. Otherwise they get launched into the wrong worktree.

My test, which hands the dispatcher a ticket with no repo label and checks that the launcher is never called:

```
$ go test ./... -run UnlabeledTicketIsNeverLaunched
ok  	github.com/kapetan-io/dispatch	0.31s
```

## Details

**State:** The PR is open with no code change since the review. I replied on the PR with the evidence above and moved this ticket to Needs Human.

**Evidence:** The dispatcher's filter removes any ticket without a repo label before launch. The test exercises that path through the dispatcher itself, so it covers the real filter and not a copy of its logic. It passes.

**Not verified:** The reviewer may be thinking of a case my test doesn't cover, such as a ticket whose repo label was removed after it was queued. I couldn't tell from the comment.

**Decision needed:** Do you agree the unlabeled case is already covered? If so, I'll resolve the thread and keep following the PR to merge. If the reviewer has a path where an unlabeled ticket reaches the launcher, I need that path, and I'll then add the skip and a test for it.
