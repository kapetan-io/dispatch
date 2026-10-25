#!/bin/bash
set -e
cp -R "$(cd "$(dirname "$0")" && pwd)/fixture/." .
git init -q .
git add -A
git -c user.name=eval -c user.email=eval@example.com commit -q -m "billing cli"
