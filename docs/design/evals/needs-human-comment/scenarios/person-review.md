You are following the pull request for ENG-290, "Skip tickets whose repo label is unknown". A person on the team reviewed it and wrote: "This should also skip tickets with no repo label at all. Otherwise they get launched into the wrong worktree."

You checked. A ticket with no repo label never reaches the launcher, because the dispatcher's filter drops it first. You proved it with a test that feeds the dispatcher an unlabeled ticket:

```
$ go test ./... -run TestTickEscalatesTicketItCannotPlaceInARepo
ok  	github.com/kapetan-io/dispatch	0.31s
```

The reviewer is a person, not a bot, so you reply with your evidence and move the ticket to Needs Human. Write the ticket comment.
