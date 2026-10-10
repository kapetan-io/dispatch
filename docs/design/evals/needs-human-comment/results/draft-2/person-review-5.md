## In plain English

A reviewer on the ENG-290 pull request asked me to make the launcher skip tickets with no repo label, so they don't start in the wrong worktree. I checked, and that can't happen today. The dispatcher drops unlabeled tickets before the launcher sees them, so the launcher never gets one to skip. I'm leaving the code as it is and need you or the reviewer to say whether that answers the concern.

An unlabeled ticket looks like this to the dispatcher.

```
ticket: "Fix flaky retry"   labels: [bug]   (no repo label)
dispatcher filter -> dropped, never handed to the launcher
```

## Details

**Review comment:** "This should also skip tickets with no repo label at all. Otherwise they get launched into the wrong worktree."

**Evidence:** I wrote a test that feeds the dispatcher an unlabeled ticket and checks it is never launched.

```
$ go test ./... -run UnlabeledTicketIsNeverLaunched
ok  	github.com/kapetan-io/dispatch	0.31s
```

**State:** No code change for this comment. I replied on the PR with the same evidence. The ticket is in Needs Human because the comment came from a person, and I don't resolve a person's review thread on my own say.

**Decision needed:** Either the dispatcher filter already covers the reviewer's case and the thread can close, or the reviewer knows a path that reaches the launcher without passing that filter. In the second case I'll add the skip, with a test through that path. Which is it?
