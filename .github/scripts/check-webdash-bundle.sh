#!/usr/bin/env bash
set -euo pipefail

changes=$(git status --porcelain --untracked-files=all -- webdash/build)
if [[ -n "$changes" ]]; then
  printf '%s\n' "$changes" >&2
  printf 'Dashboard build differs from committed assets. Run make webdash and commit webdash/build/.\n' >&2
  exit 1
fi
printf 'Dashboard build matches the committed go:embed assets.\n'
