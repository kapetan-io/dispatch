## In plain English

A teammate reviewing the pull request for ENG-290 ("Skip tickets whose repo label is unknown") asked me to also skip tickets with no repo label at all, because otherwise they get launched into the wrong worktree (the checkout directory a worker builds in). I checked, and that case can't happen. The dispatcher's filter drops an unlabeled ticket before the launcher sees it, so the extra skip would never run. Since the claim comes from a person, I replied with my evidence and left their comment open for you.

Here is the ticket shape they are worried about. It carries labels, but none names a repo.

```
ENG-999  "Fix retry backoff"
labels: [bug]
```

A test that feeds the dispatcher this kind of ticket shows it never reaches the launcher.

```
$ go test ./... -run UnlabeledTicketIsNeverLaunched
ok  	github.com/kapetan-io/dispatch	0.31s
```

## Details

**State.** The PR is open and the ticket is moving to Needs Human. Nothing in the code changed in response to the review comment.

**Evidence.** `UnlabeledTicketIsNeverLaunched` hands the dispatcher a ticket with no repo label and asserts the launcher is never called. It passes. The unknown-label skip in this PR only covers tickets that have a repo label the dispatcher doesn't recognize, which is the case that did reach the launcher.

**What I did.** I replied on the PR with the test and its output. I did not resolve the thread, because the reviewer is a person and may know of a path I didn't test.

**Decision needed.** Ask the reviewer whether the filter test answers their concern, or whether they have seen an unlabeled ticket get launched. If they have, I need the ticket or a reproduction, and I'd add the skip in the launcher as a second guard. If they agree, resolve the thread and I'll continue to merge.
