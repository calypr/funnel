#!/usr/bin/env bash
set -Eeuo pipefail
namespace=${1:?namespace is required}
log_path=${2:?port-forward log path is required}
mkdir -p "$(dirname "$log_path")"
# The forwarder outlives this command; it must not retain the runner's pipe.
kubectl -n "$namespace" port-forward service/revproxy-service 8000:80 \
  </dev/null >"$log_path" 2>&1 &
forward_pid=$!
printf '%s\n' "$forward_pid" >"$log_path.pid"
deadline=$((SECONDS + 30))
while (( SECONDS < deadline )); do
  if ! kill -0 "$forward_pid" 2>/dev/null; then
    cat "$log_path" >&2
    exit 1
  fi
  if curl --silent --output /dev/null --max-time 1 http://localhost:8000/; then
    printf 'Gen3 gateway port-forward is ready (pid=%s)\n' "$forward_pid"
    exit 0
  fi
  sleep 0.2
done
kill "$forward_pid" 2>/dev/null || true
cat "$log_path" >&2
printf 'Gen3 gateway port-forward did not become ready within 30s\n' >&2
exit 124
