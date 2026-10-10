#!/bin/bash
set -e
cp -R "$(cd "$(dirname "$0")" && pwd)/fixture/." .
chmod +x run-suite.sh
git init -q
git config user.email worker@example.com
git config user.name worker
git add -A
git commit -q -m "Commit the commit-message suite"
