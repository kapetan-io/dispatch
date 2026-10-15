// The exact texts the dispatch:build skill relies on. Its tests compare them word for word.
import type { Ticket, PR } from "dispatch";

export const STALL_NUDGE = (t: Ticket) => `${t.id} is still In Progress. Re-read the ticket and its latest comments, then keep going. If the PR is ready, move it to In Review. If you need a decision from me, move it to Needs Human and comment what you need.`;
export const RESUME_NUDGE = (t: Ticket) => `I answered on ${t.id}. Read the latest comments and continue.`;
export const COMMENT_NUDGE = (t: Ticket) => `${t.id} has a new comment. Read the ticket's comments and act on them as your build skill says.`;
export const ACTIVITY_NUDGE = (pr: PR) => `PR ${pr.url} for ${pr.ticket.id} has new activity, ${pr.activitySummary}. Read it and handle it as your build skill's step 7 says. If it asks for nothing, say nothing.`;
export const PR_NUDGE = (pr: PR) => `PR ${pr.url} for ${pr.ticket.id} has ${pr.problem}. Handle it as your build skill's step 7 says, and push.`;
