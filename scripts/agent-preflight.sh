#!/bin/sh
set -eu

printf '%s\n' "Repository: $(git rev-parse --show-toplevel)"
printf '%s\n' "Branch: $(git branch --show-current)"
printf '%s\n' "Working tree:"
git status --short
printf '%s\n' "Go: $(go version)"
