#!/usr/bin/env bash
set -Eeuo pipefail
[[ "${1:-serve}" == serve ]] || { printf 'Runner expects serve.\n' >&2; exit 2; }
# The daemon and fixture remain alive across individual local commands.
rm -f /workspace/runner-ready
mkdir -p /var/log/gen3-ci
unset DOCKER_HOST DOCKER_CONTEXT
dind dockerd --host=unix:///var/run/docker.sock > /var/log/gen3-ci/docker.log 2>&1 &
daemon_pid=$!
cleanup() {
  rm -f /workspace/runner-ready
  kill "$daemon_pid" 2>/dev/null || true
  wait "$daemon_pid" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT
for ((attempt=0; attempt<60; attempt++)); do
  if docker info >/dev/null 2>&1; then break; fi
  if ! kill -0 "$daemon_pid" 2>/dev/null; then cat /var/log/gen3-ci/docker.log; exit 1; fi
  sleep 1
done
docker info >/dev/null
touch /workspace/runner-ready
wait "$daemon_pid"
