#!/bin/sh
set -eu

for attempt in 1 2 3; do
  if go mod download; then
    exit 0
  fi
  if [ "$attempt" -lt 3 ]; then
    echo "Go module download failed; retrying ($attempt/3)" >&2
    sleep "$attempt"
  fi
done

echo "Go module download failed after three attempts" >&2
exit 1
