#!/bin/bash
set -e
cat > canned.md <<'CANNED'
# Why unknown config keys are rejected

Commit 334430a (Reject unknown config keys and add a user guide and config reference) made config.Load fail on any setting dispatch doesn't know. A typo such as max_in_progres used to fall back to the default without anyone noticing. The error now names the setting and the file. Linear was not available, so this comes from git history and docs/config.md.
CANNED
