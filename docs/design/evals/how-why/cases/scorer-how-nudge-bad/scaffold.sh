#!/bin/bash
set -e
cat > canned.md <<'CANNED'
# Nudging an idle worker

The scheduler in internal/scheduler/nudger.go polls every worker on a timer and sends a nudge whenever a worker has been quiet for five minutes. It retries forever, with exponential backoff kept in internal/scheduler/backoff.go.
CANNED
