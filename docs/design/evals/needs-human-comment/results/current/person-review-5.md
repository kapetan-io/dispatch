Tickets with no repo label never reach the launcher, so they can't be launched into the wrong worktree. The dispatcher's filter drops them first and escalates them.

I checked by feeding the dispatcher an unlabeled ticket in a test.

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

I haven't added a no-label skip to the launcher. It would be dead code, because the filter already handles that case before the launcher sees the ticket.

If you've seen an unlabeled ticket get launched, send me the ticket. That would mean the filter has a gap I haven't found, and I'd want to trace it.

I'm moving this to Needs Human. Does the test cover the case you had in mind, or is there a path I missed?
