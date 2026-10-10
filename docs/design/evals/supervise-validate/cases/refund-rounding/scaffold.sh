#!/bin/bash
set -e
cp -R "$(cd "$(dirname "$0")" && pwd)/fixture/." .
sed -i.bak "s|@LOG@|$PWD/ran.log|" repo/internal/mark/mark.go && rm repo/internal/mark/mark.go.bak
: > ran.log
cd repo
git init -q -b feature
git add -A
git -c user.name=eval -c user.email=eval@example.com commit -q -m "Add the change under review"
