#!/usr/bin/env bash
# run.sh <arm> <runs> <alias> [extra plugin-eval flags]
# Builds the arm as a plugin named <alias> in the work dir and runs every case.
set -euo pipefail
arm=$1 runs=$2 alias=$3
shift 3
here=$(cd "$(dirname "$0")" && pwd)
work=${SIZE_EVAL_WORK:?set SIZE_EVAL_WORK to a scratch directory}
out=${SIZE_EVAL_OUT:-$arm}
mkdir -p "$work" "$here/results/$out"
python3 "$here/build.py" "$arm" "$work/$alias"
cd "$work/$alias"
claude plugin eval . --eval-dir reviews --runs "$runs" --ablation none \
  --allow-tools Write --judge-model claude-opus-5-5 --keep-temp --no-publish \
  --trust-plugin --threshold 0 --json "$here/results/$out/result.json" "$@"
