#!/usr/bin/env bash
# Best-effort diagnostics must not replace the original test failure.
set -uo pipefail
output=${1:?Usage: docker-diagnostics.sh OUTPUT_DIRECTORY}
mkdir -p "$output"
timeout --kill-after=5s 15 docker info >"$output/info.txt" 2>&1 || true
timeout --kill-after=5s 15 docker ps -a --no-trunc >"$output/containers.txt" 2>&1 || true
timeout --kill-after=5s 15 docker image ls --digests >"$output/images.txt" 2>&1 || true
# Avoid dumping inspect output (which includes credential environment variables).
while IFS= read -r id; do
  [[ -n "$id" ]] || continue
  timeout --kill-after=5s 10 docker inspect --format '{{json .State}}' "$id" >"$output/$id-state.json" 2>&1 || true
  timeout --kill-after=5s 10 docker logs --tail 200 "$id" >"$output/$id.log" 2>&1 || true
done < <(timeout --kill-after=5s 15 docker ps -aq --filter label=org.testcontainers=true 2>/dev/null)
