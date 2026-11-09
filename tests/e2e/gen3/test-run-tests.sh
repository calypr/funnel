#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
runner="$script_dir/run-tests.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/gen3-tests" "$tmp/bin"
cat >"$tmp/bin/kubectl" <<'KUBECTL'
#!/usr/bin/env bash
args=("$@")
namespace=default
for ((index = 0; index < ${#args[@]}; index++)); do
  if [[ "${args[index]}" == -n ]]; then
    namespace=${args[index + 1]}
  fi
done
if [[ "${KUBECTL_STALL:-0}" == 1 ]]; then
  sleep 10
fi
case " ${args[*]} " in
  *' get pods '*)
    printf 'NAME PHASE WAITING EXIT_CODE\nfunnel-worker-fixture Running CrashLoopBackOff 17\n'
    ;;
  *' get events '*)
    printf 'LAST SEEN TYPE REASON\nnow Warning fixture-event\n'
    ;;
  *'app=funnel-worker'*)
    printf 'worker log namespace=%s\n' "$namespace"
    printf 'Authorization: Bearer header.payload.signature\n'
    printf 'jwt=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c\n'
    printf '{"access_token":"secret-value"}\n'
    printf 'AWS_SECRET_ACCESS_KEY=cloud-secret\n'
    printf "api_key='quoted-secret'\n"
    ;;
  *'app=s3-csi-node'*)
    printf 'CSI node log namespace=%s\n' "$namespace"
    ;;
  *' logs '*)
    printf 'log namespace=%s\n' "$namespace"
    printf 'Authorization: Bearer header.payload.signature\n'
    printf 'jwt=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c\n'
    printf '{"access_token":"secret-value"}\n'
    ;;
  *)
    printf 'unexpected kubectl arguments: %s\n' "$*" >&2
    exit 2
    ;;
esac
KUBECTL
chmod +x "$tmp/bin/kubectl"

failure_artifacts="$tmp/failure-artifacts"
set +e
NAMESPACE=contract \
GEN3_TEST_MAX_SECONDS=0 \
GEN3_DIAGNOSTICS_INTERVAL_SECONDS=120 \
PATH="$tmp/bin:$PATH" \
  "$runner" "$tmp/gen3-tests" "$failure_artifacts" bash -c 'printf "pytest fixture failure\nAuthorization: Bearer header.payload.signature\njwt=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c\n"; exit 23'
failure_status=$?
set -e

[[ "$failure_status" == 23 ]] || { printf 'expected test command status 23, got %s\n' "$failure_status" >&2; exit 1; }
[[ -s "$failure_artifacts/pytest.log" ]] || { printf 'pytest output artifact is missing\n' >&2; exit 1; }
[[ -s "$failure_artifacts/gen3-diagnostics.log" ]] || { printf 'diagnostics artifact is missing\n' >&2; exit 1; }
rg -q 'pytest fixture failure' "$failure_artifacts/pytest.log"
rg -q 'final diagnostics snapshot' "$failure_artifacts/gen3-diagnostics.log"
rg -q 'Gen3 test runner exit code: 23' "$failure_artifacts/gen3-diagnostics.log"
rg -q 'task pod states in workflow-pods-contract' "$failure_artifacts/gen3-diagnostics.log"
rg -q 'service pod states in contract' "$failure_artifacts/gen3-diagnostics.log"
rg -q 'worker log namespace=workflow-pods-contract' "$failure_artifacts/gen3-diagnostics.log"
rg -q 'CrashLoopBackOff 17' "$failure_artifacts/gen3-diagnostics.log"
rg -q 'CSI node log namespace=kube-system' "$failure_artifacts/gen3-diagnostics.log"
if rg -q 'eyJhbGciOi|header\.payload\.signature|secret-value|cloud-secret|quoted-secret' \
  "$failure_artifacts/pytest.log" "$failure_artifacts/gen3-diagnostics.log"; then
  printf 'a token remained in a retained test artifact\n' >&2
  exit 1
fi
rg -q '\[REDACTED JWT\]' "$failure_artifacts/pytest.log"
rg -q '\[REDACTED\]' "$failure_artifacts/gen3-diagnostics.log"

live_artifacts="$tmp/live-artifacts"
live_console="$tmp/live-console.log"
NAMESPACE=contract \
GEN3_TEST_MAX_SECONDS=0 \
GEN3_DIAGNOSTICS_INTERVAL_SECONDS=120 \
PATH="$tmp/bin:$PATH" \
  "$runner" "$tmp/gen3-tests" "$live_artifacts" python3 -c \
  'import time; print("pytest live first line", flush=True); time.sleep(2)' >"$live_console" 2>&1 &
live_runner_pid=$!
live_output_streamed=false
for ((attempt = 0; attempt < 30; attempt++)); do
  if rg -q 'pytest live first line' "$live_artifacts/pytest.log" 2>/dev/null &&
    rg -q 'pytest live first line' "$live_console" 2>/dev/null; then
    live_output_streamed=true
    break
  fi
  sleep 0.1
done
[[ "$live_output_streamed" == true ]] || { printf 'pytest output was not streamed before command completion\n' >&2; exit 1; }
if ! kill -0 "$live_runner_pid" 2>/dev/null; then
  printf 'live-output fixture completed before its first line was observed\n' >&2
  exit 1
fi
wait "$live_runner_pid"

signal_artifacts="$tmp/signal-artifacts"
signal_console="$tmp/signal-console.log"
signal_child_marker="$tmp/signal-child-survived"
NAMESPACE=contract \
GEN3_TEST_MAX_SECONDS=0 \
GEN3_DIAGNOSTICS_INTERVAL_SECONDS=120 \
PATH="$tmp/bin:$PATH" \
  "$runner" "$tmp/gen3-tests" "$signal_artifacts" python3 -c \
  'import pathlib, subprocess, sys, time; subprocess.Popen([sys.executable, "-c", "import pathlib, sys, time; time.sleep(2); pathlib.Path(sys.argv[1]).write_text(\"survived\")", sys.argv[1]]); print("pytest fixture signal", flush=True); time.sleep(5)' \
  "$signal_child_marker" >"$signal_console" 2>&1 &
signal_runner_pid=$!
signal_output_started=false
for ((attempt = 0; attempt < 30; attempt++)); do
  if rg -q 'pytest fixture signal' "$signal_console" 2>/dev/null; then
    signal_output_started=true
    break
  fi
  sleep 0.1
done
[[ "$signal_output_started" == true ]] || { printf 'signal fixture did not start its test process\n' >&2; exit 1; }
kill -TERM "$signal_runner_pid"
set +e
wait "$signal_runner_pid"
signal_status=$?
set -e
[[ "$signal_status" == 143 ]] || { printf 'expected TERM status 143, got %s\n' "$signal_status" >&2; exit 1; }
rg -q 'final diagnostics snapshot' "$signal_artifacts/gen3-diagnostics.log"
rg -q 'Gen3 test runner exit code: 143' "$signal_artifacts/gen3-diagnostics.log"
sleep 2.2
[[ ! -e "$signal_child_marker" ]] || { printf 'TERM left a pytest child running during final diagnostics\n' >&2; exit 1; }

timeout_artifacts="$tmp/timeout-artifacts"
child_marker="$tmp/timeout-child-survived"
set +e
NAMESPACE=contract \
GEN3_TEST_MAX_SECONDS=1 \
GEN3_DIAGNOSTICS_INTERVAL_SECONDS=120 \
PATH="$tmp/bin:$PATH" \
  "$runner" "$tmp/gen3-tests" "$timeout_artifacts" python3 -c \
  'import pathlib, subprocess, sys, time; subprocess.Popen([sys.executable, "-c", "import pathlib, sys, time; time.sleep(2); pathlib.Path(sys.argv[1]).write_text(\"survived\")", sys.argv[1]]); print("pytest fixture timeout", flush=True); time.sleep(5)' \
  "$child_marker"
timeout_status=$?
set -e

[[ "$timeout_status" == 124 ]] || { printf 'expected bounded timeout status 124, got %s\n' "$timeout_status" >&2; exit 1; }
rg -q 'pytest fixture timeout' "$timeout_artifacts/pytest.log"
rg -q 'final diagnostics snapshot' "$timeout_artifacts/gen3-diagnostics.log"
rg -q 'Gen3 test runner exit code: 124' "$timeout_artifacts/gen3-diagnostics.log"
sleep 2.2
[[ ! -e "$child_marker" ]] || { printf 'timed-out pytest child survived the session bound\n' >&2; exit 1; }

stalled_artifacts="$tmp/stalled-artifacts"
stalled_started=$SECONDS
set +e
NAMESPACE=contract \
GEN3_TEST_MAX_SECONDS=5 \
GEN3_DIAGNOSTICS_INTERVAL_SECONDS=120 \
GEN3_FINAL_DIAGNOSTICS_MAX_SECONDS=1 \
KUBECTL_STALL=1 \
PATH="$tmp/bin:$PATH" \
  "$runner" "$tmp/gen3-tests" "$stalled_artifacts" bash -c 'printf "pytest failure with stalled diagnostics\n"; exit 29'
stalled_status=$?
set -e
stalled_elapsed=$((SECONDS - stalled_started))
[[ "$stalled_status" == 29 ]] || { printf 'expected test command status 29, got %s\n' "$stalled_status" >&2; exit 1; }
[[ "$stalled_elapsed" -le 8 ]] || { printf 'stalled diagnostics exceeded their bounded cleanup window (%ss)\n' "$stalled_elapsed" >&2; exit 1; }
rg -q 'final snapshot exited with status 124' "$stalled_artifacts/gen3-diagnostics.log"
rg -q 'Gen3 test runner exit code: 29' "$stalled_artifacts/gen3-diagnostics.log"

printf 'run-tests contract passed for failure artifacts, redaction, live output, and process cleanup\n'
