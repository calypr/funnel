#!/usr/bin/env bash
# Downloads are retried; test execution is never retried by this helper.
set -euo pipefail

if [[ $# -eq 0 ]]; then
  echo "Usage: $0 IMAGE [IMAGE ...]" >&2
  exit 2
fi
for image in "$@"; do
  for attempt in 1 2 3; do
    if timeout --kill-after=10s 180 docker pull "$image"; then
      break
    fi
    if [[ "$attempt" -eq 3 ]]; then
      echo "Failed to pull $image after $attempt attempts" >&2
      exit 1
    fi
    echo "Retrying download of $image ($attempt/3)" >&2
    sleep "$((attempt * 5))"
  done
done
