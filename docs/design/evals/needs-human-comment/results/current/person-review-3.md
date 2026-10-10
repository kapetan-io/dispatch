Thanks for the review. I checked the unlabeled case and I don't think it can happen, so I haven't changed the code.

A ticket with no repo label never reaches the launcher. The dispatcher's filter drops it before the launcher sees it, so the unknown-label skip in this PR is the only guard the launcher needs. I fed the dispatcher an unlabeled ticket to confirm:

```
$ go test ./... -run UnlabeledTicketIsNeverLaunched
ok  	github.com/kapetan-io/dispatch	0.31s
```

The test is in the PR. If you've seen an unlabeled ticket get launched into the wrong worktree, I'm missing the path, and a repro would settle it. Otherwise I'd leave the skip scoped to unknown labels.

Moving this to Needs Human, since you and I disagree and I won't resolve that by myself. Can you tell me whether the test covers the case you meant?
