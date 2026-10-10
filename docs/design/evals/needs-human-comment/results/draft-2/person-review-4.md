## In plain English

A reviewer on the ENG-290 pull request asked me to make the launcher skip tickets that have no repo label, saying they would otherwise start in the wrong worktree. The launcher is the piece that picks a repo checkout and starts a worker in it. I checked, and the dispatcher's filter already drops unlabeled tickets before the launcher sees them, so the change looks unnecessary. A person made the claim and I disagree with it, so I'm leaving the call to you rather than resolving it myself.

Here is the input I tested. A ticket with no labels at all:

```
ticket: ENG-999
labels: []
```

The dispatcher never hands it to the launcher.

```
$ go test ./... -run UnlabeledTicketIsNeverLaunched
ok  	github.com/kapetan-io/dispatch	0.31s
```

## Details

**Review comment:** "This should also skip tickets with no repo label at all. Otherwise they get launched into the wrong worktree."

**Evidence:** `UnlabeledTicketIsNeverLaunched` feeds the dispatcher an unlabeled ticket and asserts the launcher is never called. It passes, so the filter upstream of the launcher already covers this case.

**What I haven't ruled out:** the reviewer may be thinking of a path that bypasses the dispatcher filter, such as a manual launch. I found none in the code I traced, but I can't tell what they had in mind.

**Decision for you:** either tell the reviewer the existing filter covers it, or ask them which path they think reaches the launcher unlabeled. If they name one, I'll write a failing test for it and fix the code. I'm leaving the PR unchanged until then.
