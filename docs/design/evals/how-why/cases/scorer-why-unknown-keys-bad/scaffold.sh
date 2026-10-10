#!/bin/bash
set -e
cat > canned.md <<'CANNED'
# Why unknown config keys are rejected

The TOML library crashes on unknown keys, so the code in internal/config/config.go returns an error to avoid a panic. It keeps the parser fast.
CANNED
