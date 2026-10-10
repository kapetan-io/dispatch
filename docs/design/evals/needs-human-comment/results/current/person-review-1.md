Tickets with no repo label can't be launched into the wrong worktree, because they never reach the launcher. The dispatcher's filter drops them first, so a skip rule in the launcher would never run.

I checked this with a test that feeds the dispatcher an unlabeled ticket:

```
$ go test ./... -run UnlabeledTicketIsNeverLaunched
ok  	github.com/kapetan-io/dispatch	0.31s
```

I haven't changed the PR. The only case ENG-290 covers is a repo label that is set but unknown, and that one is skipped.

If you've seen an unlabeled ticket get launched, I'd like to know how it got past the filter. That would mean my test misses a path. If not, I think the PR is right as it stands. I'm moving this to Needs Human because the call on the review is yours.
