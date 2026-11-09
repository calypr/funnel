#!/usr/bin/env bash
set -Eeuo pipefail
script_dir=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
cleanup() {
  if [[ -f "$tmp/forward.log.pid" ]]; then kill "$(cat "$tmp/forward.log.pid")" 2>/dev/null || true; fi
  rm -rf "$tmp"
}
trap cleanup EXIT
mkdir -p "$tmp/bin"
printf '#!/bin/sh\nprintf "forwarder is alive\\n"\nexec sleep 30\n' >"$tmp/bin/kubectl"
printf '#!/bin/sh\nexit 0\n' >"$tmp/bin/curl"
chmod +x "$tmp/bin/"*
PATH="$tmp/bin:$PATH" python3 "$script_dir/run-command.py" "$tmp" "$tmp/runner.log" 2 \
  bash "$script_dir/start-port-forward.sh" contract "$tmp/forward.log"
kill -0 "$(cat "$tmp/forward.log.pid")"
printf 'PASS: exposure returns while its forwarder remains alive without holding the runner pipe\n'
