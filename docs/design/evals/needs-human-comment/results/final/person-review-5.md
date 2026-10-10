## In plain English

A reviewer on the PR for ENG-290 asked me to also skip tickets that have no repo label, because they worried those would launch into the wrong worktree. I don't think that can happen. The dispatcher drops an unlabeled ticket before the launcher ever sees it, so the launcher has nothing to skip.

Here is the case the reviewer is worried about.

```
ENG-999  "Fix flaky retry test"
labels:  (none)
```

The dispatcher's filter drops this ticket on its first pass. I disagree with the review comment and made no code change for it. The reviewer is a person, so I'm not resolving the thread. Please read my evidence and either tell the reviewer I'm right or tell me what they are seeing that I'm missing.

## Details

**Evidence.** I wrote a test that feeds the dispatcher an unlabeled ticket and checks what happens to it.

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

**Status.** The PR is unchanged and I'm moving this ticket to Needs Human. The case the reviewer raised is already covered, and this ticket only adds the skip for a label that names an unknown repo.

**Decision needed.** Reply on the PR thread yourself, or tell me to reply with this evidence. If the reviewer has a path where an unlabeled ticket reaches the launcher, I'd like to see it, since that would be a bug in the filter.
