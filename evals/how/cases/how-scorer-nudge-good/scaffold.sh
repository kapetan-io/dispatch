#!/bin/bash
set -e
cat > canned.md <<'CANNED'
# Nudging an idle worker

A tick builds a reconcile.Input and calls Plan in internal/reconcile/reconcile.go. For an In Progress ticket whose worker is idle, watch returns a Nudge action with StallNudge text. It first compares the ticket's earlier nudge count in the cache with Input.NudgeLimit. At the limit it returns an Escalate action instead, which moves the ticket to Needs Human. Worker state comes from internal/worker.
CANNED
