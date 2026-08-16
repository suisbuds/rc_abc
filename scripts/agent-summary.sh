#!/bin/sh
set -eu

task=${1:?task slug is required}
timestamp=$(date '+%Y-%m-%d-%H%M%S')
target="docs/session/${timestamp}-${task}.md"

mkdir -p docs/session
cp docs/session/TEMPLATE.md "$target"
printf '%s\n' "$target"
