Thanks for the review. I checked the unlabeled case, and I don't think it can happen today, so I haven't changed the code.

The dispatcher's filter drops any ticket with no repo label before the launcher sees it, so an unlabeled ticket never reaches a worktree. I fed the dispatcher an unlabeled ticket in a test and it was never launched:

```
$ go test ./... -run UnlabeledTicketIsNeverLaunched
ok  	github.com/kapetan-io/dispatch	0.31s
```

That test is in the PR. A second skip in the launcher would duplicate the filter's check and give two places to keep in sync.

I'm moving this to Needs Human because I disagree with a person's review and don't want to resolve it myself. If you have a path where an unlabeled ticket gets past the filter, send it and I'll add a test for it and the skip. Otherwise, please confirm the PR can merge as is.
