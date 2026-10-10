Checked this against the code. Tickets with no repo label never reach the launcher, so they can't land in the wrong worktree. The dispatcher's filter drops them before the launcher is called, and it escalates them to Needs Human instead of launching.

I proved it with a test that feeds the dispatcher an unlabeled ticket and asserts it is escalated rather than launched:

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

Adding a second skip for the missing-label case inside the launcher would be dead code, since nothing can reach it. It would also give two places that decide what to do with an unplaceable ticket, which can drift apart. I kept this PR to the unknown-label case.

If I'm missing a path where an unlabeled ticket gets to the launcher (a manual launch, a different entry point), point me at it and I'll write a test for that path. Otherwise I think the PR is good as is.

Moving this to Needs Human, since the review comment is from a person and I can't resolve it myself.
