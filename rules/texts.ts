// The exact texts the dispatch:build skill relies on. Its tests compare them word for word.
import type { Ticket, PR } from "dispatch";

export const RESUME_NUDGE = (t: Ticket) => `I answered on ${t.id}. Read the latest comments and continue.`;
export const ACTIVITY_NUDGE = (pr: PR) => `PR ${pr.url} for ${pr.ticket.id} has new activity, ${pr.activitySummary}. Read it and handle it as your build skill's step 7 says. If it asks for nothing, say nothing.`;
export const PR_NUDGE = (pr: PR) => `PR ${pr.url} for ${pr.ticket.id} has ${pr.problem}. Handle it as your build skill's step 7 says, and push.`;
