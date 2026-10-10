Thanks for the review. I checked the unlabeled case and I don't think it reaches the launcher, so I'm not changing the code yet.

The dispatcher's filter drops any ticket with no repo label before the launcher sees it. The dispatcher escalates that ticket to Needs Human instead, so it never gets a worktree. A test that feeds the dispatcher an unlabeled ticket confirms it:

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

The skip in this PR covers the other case, where a label exists but names a repo we don't know. Adding an unlabeled check to the launcher would duplicate the filter and leave two places that decide what "no repo" means.

If you've seen an unlabeled ticket get launched, I'd like the ticket ID so I can reproduce it. Otherwise I think this is fine as is. I'm moving the ticket to Needs Human because the call is yours. Tell me which way you want it.
