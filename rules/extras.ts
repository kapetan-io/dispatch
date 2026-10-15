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
  each:   "github.pr",
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
  event:  "github.check.completed",
  if:     (run, ctx) => run.conclusion === "failure" && run.head === run.pr.head && !run.pr.closed
             && ctx.herdr.worker(run.pr.ticket).state === "idle" && stoppedSinceLastNudge(run.pr.ticket, ctx),
  then:   (run, ctx) => ctx.herdr.queueNudge(run.pr.ticket, `Check ${run.name} failed on ${run.pr.url}: ${run.url}. Fix it and push.`,
             { once: ["check", run.id] }),
  shadow: true,
});

// Once per issue. The new ticket links back to the issue, which makes this false, and
// the key keeps a second ticket from being opened before Linear shows the first.
whenever({
  name: "open-ticket-for-sentry-issue",
  each: "sentry.issue",
  if:   (issue, ctx) => issue.events24h > 100 && ctx.linear.ticketLinking(issue.url) === undefined,
  then: (issue, ctx) => ctx.linear.queueCreate({ title: `Sentry: ${issue.title}`, optIn: true, links: [issue.url] },
           { once: ["sentry", issue.url] }),
});

// A leased tree nobody has used for two hours, for a ticket no other rule looks after.
// The wait runs from the newest of the facts that make it true.
const orphanedSince = (tree, ctx) =>
  Math.max(tree.leasedSince, ctx.herdr.workerIn(tree).goneSince, tree.holder.statusSince);
whenever({
  name: "notify-orphaned-tree",
  each: "treehouse.tree",
  if:   (tree, ctx) => tree.leased && ctx.herdr.workerIn(tree).state === "none"
           && !tree.holder.paused && !tree.holder.finished
           && tree.holder.status !== "in_progress" && tree.holder.status !== "in_review"
           && ctx.now >= orphanedSince(tree, ctx) + hours(2),
  then: (tree, ctx) => ctx.notify.queueSend(tree.holder, `${tree.path} is leased to ${tree.holder.id} but no worker uses it`,
           { once: ["orphaned", tree.path, orphanedSince(tree, ctx)] }),
});
