// The exact nudge texts. The PR nudges start with /dispatch:pr-respond so the worker
// loads that skill fresh, and the Go tests compare them word for word.
import type { Ticket, PR } from "dispatch";

export const ACTIVITY_NUDGE = (pr: PR) => `/dispatch:pr-respond PR ${pr.url} for ${pr.ticket.id} has new activity, ${pr.activitySummary}. Load the dispatch:pr-respond skill with the Skill tool, then read it and handle it. If it asks for nothing, say nothing.`;
export const PR_NUDGE = (pr: PR) => `/dispatch:pr-respond PR ${pr.url} for ${pr.ticket.id} has ${pr.problem}. Load the dispatch:pr-respond skill with the Skill tool, then handle it, and push.`;
