# Sentry Error History

## What this source contains

The archive of things that went wrong: grouped issues with first/last seen, counts and affected releases, plus events, releases and comments. For defensive code it often holds the direct motivation. Its main value is temporal correlation: "issue X appeared 2024-01-02, peaked at 500/day, vanished after the release that shipped the check."

## How to search it

Use the Sentry MCP. Search issues using the exception classes the target handles, its function names, error strings it checks, and its file path. For a suspect issue check first seen, last seen, affected releases and frequency trajectory against the target's ship date, then open one full event: does the stack trace pass through the target, and do tags and breadcrumbs match what it defends against?

Treat Seer's root-cause analyses as hypotheses, not evidence. Seer can sound confident and be wrong, so cite the events, stack traces and timestamps.

## What good evidence looks like here

- An issue whose **first seen** is shortly before the target's PR and **last seen** shortly after, suggesting the target addressed this error
- Stack traces that pass through or land on the target function, showing the exact failure mode being defended against
- A comment on the issue from the PR author describing the fix
- The target's PR description or commit message referencing a Sentry issue URL or ID
- An issue with high event counts that stops after the release containing the target

## Common pitfalls

- **Grouping drift.** Sentry groups errors by fingerprint. Refactors or renames can track the "same" error under a new issue ID. If an issue ends abruptly, the error may have just been regrouped. Check for new issues immediately after.
- **Release correlation is noisy.** A release contains many commits. An issue stopping at v2.14.0 doesn't prove the target fixed it. Another change in the same release might have. Cross-reference with the target's exact commit.
- **Silent fixes.** Sometimes the error stops because upstream changed, not because of the defensive code. The correlation suggests the fix. It doesn't prove authorship.
- **Resolved != fixed.** Issues can be marked "resolved" manually without any code change. Treat `resolved` as a human marker, not evidence that code fixed it.
- **Sampling.** Some projects sample events aggressively. A low event count may just mean high sampling, not a rare error. If in doubt, note the gap.

## What to return

For each relevant issue:
- Issue ID and title
- Project and organization
- First seen / last seen timestamps
- Event count (and sampling rate if known)
- Affected releases
- A representative stack trace snippet showing relevance to the target (verbatim excerpt, not summary)
- First/last-seen correlation with the target's ship date
- Link to the issue
- Any author comments or resolution notes
