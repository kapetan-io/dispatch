# Commit message suite

Tests whether the commit-message skill's wording gives the right shape of message.

## Arms

- `arms/current.md` is the wording on main.

## Cases

`cases.json` lists six diffs and what a good message looks like for each.

## Graders, per run

- `subject`. The subject line is imperative and under 50 characters.
- `body`. A body is present exactly when `expect` says one is needed.
- `cause`. When a body is expected, it gives the reason and not only a description of the diff.

## Pass bar (fixed before any run)

The wording is adopted only if all hold on one attempt of 5 runs per case, and again on a confirmation rerun.

1. Every case passes `subject`, `body` and `cause` in 4 of 5 runs.
2. Each grader passes in 90% of runs, all cases pooled.
3. Cases marked `heldout` meet 1 and 2 on their own.

The control is `arms/always-body.md`, which tells the writer to always add a body. It must fail `body` in more than half its runs on the three no-body cases. If it does not, no result is read.

Budget: 5 runs per case, 5 attempts, $40 in all.

