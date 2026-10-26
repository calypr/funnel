#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  printf 'usage: %s <gen3-test-root> <artifacts-dir> <pytest-command> [args...]\n' "$0" >&2
  exit 2
}

[[ $# -ge 3 ]] || usage
: "${NAMESPACE:?NAMESPACE is required}"

test_root=$1
artifacts_dir=$2
shift 2
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

[[ -d "$test_root" ]] || { printf 'Gen3 test root is not a directory: %s\n' "$test_root" >&2; exit 2; }
[[ "$artifacts_dir" != '' ]] || { printf 'artifacts directory must not be empty\n' >&2; exit 2; }

test_root=$(cd "$test_root" && pwd)
mkdir -p "$artifacts_dir"
artifacts_dir=$(cd "$artifacts_dir" && pwd)

test_max_seconds=${GEN3_TEST_MAX_SECONDS:-0}
diagnostics_interval_seconds=${GEN3_DIAGNOSTICS_INTERVAL_SECONDS:-15}
final_snapshot_max_seconds=${GEN3_FINAL_DIAGNOSTICS_MAX_SECONDS:-20}
[[ "$test_max_seconds" =~ ^[0-9]+$ ]] && (( test_max_seconds >= 0 )) || {
  printf 'GEN3_TEST_MAX_SECONDS must be a nonnegative integer (0 disables the deadline)\n' >&2
  exit 2
}
[[ "$diagnostics_interval_seconds" =~ ^[0-9]+$ ]] && (( diagnostics_interval_seconds >= 1 && diagnostics_interval_seconds <= 120 )) || {
  printf 'GEN3_DIAGNOSTICS_INTERVAL_SECONDS must be an integer in [1, 120]\n' >&2
  exit 2
}
[[ "$final_snapshot_max_seconds" =~ ^[0-9]+$ ]] && (( final_snapshot_max_seconds >= 1 && final_snapshot_max_seconds <= 20 )) || {
  printf 'GEN3_FINAL_DIAGNOSTICS_MAX_SECONDS must be an integer in [1, 20]\n' >&2
  exit 2
}

collector="$script_dir/collect-task-diagnostics.sh"
command_runner="$script_dir/run-command.py"
[[ -f "$collector" ]] || { printf 'diagnostics collector is missing: %s\n' "$collector" >&2; exit 2; }
[[ -f "$command_runner" ]] || { printf 'command runner is missing: %s\n' "$command_runner" >&2; exit 2; }
command -v python3 >/dev/null || { printf 'python3 is required\n' >&2; exit 2; }

pytest_log="$artifacts_dir/pytest.log"
diagnostics_log="$artifacts_dir/gen3-diagnostics.log"
collector_pid=
test_runner_pid=

finish() {
  local status=$?
  trap - EXIT

  if [[ -n "$test_runner_pid" ]]; then
    if kill -0 "$test_runner_pid" 2>/dev/null; then
      kill -TERM "$test_runner_pid" 2>/dev/null || true
    fi
    wait "$test_runner_pid" 2>/dev/null || true
    test_runner_pid=
  fi
  if [[ -n "$collector_pid" ]]; then
    if kill -0 "$collector_pid" 2>/dev/null; then
      kill -TERM "$collector_pid" 2>/dev/null || true
    fi
    wait "$collector_pid" 2>/dev/null || true
    collector_pid=
  fi

  printf '\n--- final diagnostics snapshot ---\n' >>"$diagnostics_log"
  set +e
  if (( status == 0 )); then
    GEN3_LOG_APPEND=1 python3 "$command_runner" "$test_root" "$diagnostics_log" "$final_snapshot_max_seconds" \
      bash "$collector" snapshot "$NAMESPACE" >/dev/null
  else
    GEN3_LOG_APPEND=1 python3 "$command_runner" "$test_root" "$diagnostics_log" "$final_snapshot_max_seconds" \
      bash "$collector" snapshot "$NAMESPACE" failure >/dev/null
  fi
  snapshot_status=$?
  set -e
  if (( snapshot_status != 0 )); then
    printf 'final snapshot exited with status %s\n' "$snapshot_status" >>"$diagnostics_log"
  fi
  printf '\nGen3 test runner exit code: %s\n' "$status" >>"$diagnostics_log"

  exit "$status"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

collector_limit=0
if (( test_max_seconds > 0 )); then
  collector_limit=$((test_max_seconds + 60))
fi
printf 'Gen3 tests starting. Session cap: %ss (0 means unlimited). Artifacts: %s\n' "$test_max_seconds" "$artifacts_dir"
GEN3_LOG_APPEND=1 python3 "$command_runner" "$test_root" "$diagnostics_log" "$collector_limit" \
  bash "$collector" watch "$NAMESPACE" "$diagnostics_interval_seconds" "$test_max_seconds" \
  >/dev/null &
collector_pid=$!

python3 "$command_runner" "$test_root" "$pytest_log" "$test_max_seconds" "$@" &
test_runner_pid=$!
set +e
wait "$test_runner_pid"
test_status=$?
set -e
test_runner_pid=
exit "$test_status"
