#!/bin/sh
set -eu

message_file=${1:?commit message file is required}
first_line=$(sed -n '1p' "$message_file")

case "$first_line" in
  Merge\ *|Revert\ *) exit 0 ;;
esac

pattern='^(feat|fix|docs|test|refactor|perf|build|ci|chore)(\([a-z0-9._/-]+\))?!?: .+'

if ! printf '%s\n' "$first_line" | grep -Eq "$pattern"; then
  printf '%s\n' "Commit message must follow Conventional Commits." >&2
  printf '%s\n' "Example: feat(worker): recover expired leases" >&2
  exit 1
fi
