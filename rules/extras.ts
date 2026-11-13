import { whenever, on, hours } from "dispatch";
import { stoppedSinceLastNudge } from "./shared";

// Examples of rules beyond today's behavior. Some need plugins dispatch doesn't have
// yet, and none is in dispatch.ts, so none runs until you import it.

const REVIEW_CHANNEL = "#eng-review";

// Waiting a day. Approved, pushed or merged within the day makes this false, and a
// push starts the day over. New, so it only reports what it would post.
const waitingSince = (pr) => Math.max(pr.readySince, pr.headSince);
whenever({
  name:   "post-waiting-review",
  each:   "pr",
  if:     (pr, ctx) => !pr.closed && !pr.draft && pr.ready && !pr.approved && ctx.now >= waitingSince(pr) + hours(24),
  then:   (pr, ctx) => ctx.slack.queuePost(REVIEW_CHANNEL, `${pr.ticket.id} has waited a day for review: ${pr.url}`,
             { once: ["waiting-review", pr.head, waitingSince(pr)] }),
  shadow: true,
});

// Runs for each check run that completes after dispatch starts, never for older ones.
// nudge-pr-needs-work already nudges about failed checks, so this one only reports.
// Like every nudge rule, it waits until the worker has stopped since its last nudge.
on({
  name:   "nudge-failed-check",
  event:  "check.completed",
  if:     (run, ctx) => run.conclusion === "failure" && run.head === run.pr.head && !run.pr.closed
             && ctx.worker.of(run.pr.ticket).state === "idle" && stoppedSinceLastNudge(run.pr.ticket, ctx),
  then:   (run, ctx) => ctx.worker.queueNudge(run.pr.ticket, `Check ${run.name} failed on ${run.pr.url}: ${run.url}. Fix it and push.`,
             { once: ["check", run.id] }),
  shadow: true,
});

// Once per issue. The new ticket links back to the issue, which makes this false, and
// the key keeps a second ticket from being opened before Linear shows the first.
whenever({
  name: "open-ticket-for-sentry-issue",
  each: "sentry.issue",
  if:   (issue, ctx) => issue.events24h > 100 && ctx.ticket.ticketLinking(issue.url) === undefined,
  then: (issue, ctx) => ctx.ticket.queueCreate({ title: `Sentry: ${issue.title}`, optIn: true, links: [issue.url] },
           { once: ["sentry", issue.url] }),
});
