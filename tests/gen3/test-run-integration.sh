#!/usr/bin/env bash
set -Eeuo pipefail
src=$(cd "$(dirname "$0")" && pwd)
py=${PYTHON:-$(command -v python3)}
export PATH="$(dirname "$BASH"):$PATH"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/gen3-lifecycle-test.XXXXXX")
trap 'status=$?; if (( status == 0 )); then rm -rf "$tmp"; else printf "Failure artifacts: %s\n" "$tmp" >&2; fi' EXIT
[[ -x "$py" ]] || { echo 'Python 3 required; set PYTHON' >&2; exit 2; }
fail() { echo "FAIL: $*" >&2; exit 1; }
fixture() {
  n=$1; f=$tmp/$n
  mkdir -p "$f/bin" "$f/work/gen3-code-vigil/gen3-integration-tests" "$f/work/gen3-gitops-ci/ci/default/values" "$f/work/tests/gen3" "$f/state" "$f/artifacts"
  cp "$src/run-integration.sh" "$f/run-integration.sh"
  cp "$src/run-command.py" "$src/redact_logs.py" "$f/"
  printf '#!/bin/sh\nexit 0\n' >"$f/describe-environment.sh"
  chmod +x "$f/run-integration.sh"
  ln -s "$py" "$f/bin/python3"
  echo '#!/bin/sh
echo "Python 3.13.9"' >"$f/bin/python"
  cat >"$f/bin/git" <<'EOF'
#!/bin/sh
case "$*" in *gen3-code-vigil*) echo 87da170a9a1cb099cfd1aded9402bc323201d6bc;; *gen3-gitops-ci*) echo 19a687205de78d985416f44ad89c79bd2e66dab8;; *) exit 2;; esac
EOF
  echo '#!/bin/sh
echo v4.3.1' >"$f/bin/helm"
  for x in docker aws curl jq yq; do printf '#!/bin/sh\nexit 0\n' >"$f/bin/$x"; done
  cat >"$f/bin/java" <<'EOF'
#!/bin/sh
printf '%s\n' 'openjdk version "21.0.12"' >&2
EOF
  cat >"$f/bin/kubectl" <<'EOF'
#!/bin/sh
[ "$1" = --kubeconfig ] && shift 2
[ "$*" = 'get --raw=/readyz' ] && echo readyz >>"$EVENTS"
EOF
  echo '#!/bin/sh
echo kind-delete >>"$EVENTS"' >"$f/bin/kind"
  cat >"$f/bin/uv" <<'EOF'
#!/bin/bash
case "$*" in *pytest*) echo pytest-end >>"$EVENTS";; *poetry*) echo python-start >>"$EVENTS"; sleep 4; echo python-end >>"$EVENTS";; esac
EOF
  printf '#!/bin/sh\nuv poetry install\n' >"$f/prepare-python.sh"
  printf '#!/bin/sh\nexit 0\n' >"$f/prepare-test-tools.sh"
  printf '#!/bin/sh\necho executable-check >>"$EVENTS"\nif [ "$TEST_CASE" = foreign-executable ]; then exit 73; fi\n' >"$f/verify-service-executables.sh"
  echo "pass" >"$f/cache-fixture-images.py"
  echo "pass" >"$f/configure-tests.py"
  cat >"$f/build-and-start-kind.sh" <<'EOF'
#!/bin/bash
echo build-start >>"$EVENTS"
mkdir -p "$FUNNEL_CI_STATE_DIR"
echo fake >"$FUNNEL_CI_KUBECONFIG"
echo FUNNEL_CI_CLUSTER_OWNED=true >>"$GITHUB_ENV"
if [[ "$TEST_CASE" != missing-marker ]]; then touch "$FUNNEL_CI_KIND_READY_FILE"; fi
sleep 4
echo build-end >>"$EVENTS"
EOF
  cat >"$f/setup-environment.sh" <<'EOF'
#!/bin/bash
echo storage-start >>"$EVENTS"
if [[ "$TEST_CASE" == branch-fail ]]; then
 for _ in {1..100}; do
  if grep -q '^auth-start$' "$EVENTS"; then echo storage-end-23 >>"$EVENTS"; exit 23; fi
  sleep 0.05
 done
 exit 99
fi
if [[ "$TEST_CASE" == cache-fail ]]; then sleep 6; echo storage-end-24 >>"$EVENTS"; exit 24; fi
sleep 1
echo storage-end >>"$EVENTS"
EOF
  cat >"$f/bootstrap-services.sh" <<'EOF'
#!/bin/bash
event() { echo "$1" >>"$EVENTS"; }
case "$1" in
prepare-auth)
 if [[ "$TEST_CASE" == branch-fail ]]; then
  trap 'event auth-term; exit 0' TERM
  event auth-start
  sleep 30 & wait $!
 fi
 event auth-start
 sleep 2; event auth-end;;
users) event users-start; sleep 1; event users-end;;
deploy) event deploy-start; echo deploy-end >>"$EVENTS";;
esac
EOF
  cat >"$f/expose-and-verify-environment.sh" <<'EOF'
#!/bin/bash
echo expose >>"$EVENTS"
sleep 600 </dev/null >/dev/null 2>&1 &
printf '%s\n' "$!" >"$FUNNEL_CI_ARTIFACTS_DIR/port-forward.log.pid"
EOF
  cat >"$f/run-tests.sh" <<'EOF'
#!/bin/bash
shift 2
"$@"
EOF
  echo '#!/bin/bash
echo diagnostics >>"$EVENTS"' >"$f/collect-task-diagnostics.sh"
  touch "$f/work/gen3-code-vigil/gen3-integration-tests/pyproject.toml" "$f/work/gen3-code-vigil/gen3-integration-tests/poetry.lock" "$f/work/gen3-gitops-ci/ci/default/values/values.yaml" "$f/work/tests/gen3/values-ci.yaml"
  chmod +x "$f/bin/"* "$f"/*.sh
}
run() {
  n=$1; budget=$2; f=$tmp/$n; ws=$f/work
  touch "$f/events" "$f/github-env" "$f/github-output"; : >"$f/events"
  deadline=0
  if (( budget > 0 )); then deadline=$(($(date +%s)+budget)); fi
  set +e
  env PATH="$f/bin:$PATH" HOME="$f/home" EVENTS="$f/events" TEST_CASE="$n" \
   GITHUB_WORKSPACE="$ws" GH_WORKSPACE="$ws" TEST_REPO="$ws/gen3-code-vigil" TEST_REPO_SHA=87da170a9a1cb099cfd1aded9402bc323201d6bc \
   WORKING_DIR=gen3-code-vigil/gen3-integration-tests GEN3_GITOPS_REPO="$ws/gen3-gitops-ci" GEN3_GITOPS_SHA=19a687205de78d985416f44ad89c79bd2e66dab8 \
   FUNNEL_CI_ARTIFACTS_DIR="$f/artifacts" FUNNEL_CI_STATE_DIR="$f/state" FUNNEL_CI_KUBECONFIG="$f/state/kubeconfig" FUNNEL_CI_KIND_READY_FILE="$f/state/kind-ready" FUNNEL_CI_BUILD_PLATFORM=linux/arm64 \
   FUNNEL_CI_DEADLINE_EPOCH="$deadline" FUNNEL_CI_STARTED_EPOCH="$(date +%s)" GITHUB_ENV="$f/github-env" GITHUB_OUTPUT="$f/github-output" \
   REPO=funnel REPO_FN=calypr/funnel BRANCH=main PR_NUM=1 COMMIT_SHA=deadbeef RUN_NUM=1 RUN_ID=1 ATTEMPT_NUM=1 CI_ENV=default HELM_BRANCH=master \
   QUAY_ORG=ohsu-comp-bio QUAY_REPO=funnel EXTERNAL_TO_CTDS=true HOSTNAME=localhost:8000 HOSTNAME_PROTOCOL=http SERVICE_TO_TEST=gen3_workflow \
   NAMESPACE=gen3 GEN3_CHART_VERSION=0.3.110 GEN3_VALUES_FILE="$ws/tests/gen3/values-ci.yaml" "$f/run-integration.sh" >"$f/output" 2>&1
  status=$?
  set -e
  echo "$status" >"$f/status"
}
line() { grep -n -m1 -x "$2" "$tmp/$1/events" | cut -d: -f1 || fail "$1 missing $2"; }
fixture happy
run happy 0
[[ $(cat "$tmp/happy/status") == 0 ]] || { cat "$tmp/happy/output" >&2; fail 'happy lifecycle failed'; }
(( $(line happy python-start) < $(line happy auth-start) && $(line happy auth-start) < $(line happy build-end) )) || fail 'auth did not overlap bootstrap/build'
(( $(line happy storage-end) < $(line happy users-start) && $(line happy auth-end) < $(line happy users-start) )) || fail 'users began before storage/auth finished'
(( $(line happy users-end) < $(line happy deploy-start) && $(line happy build-end) < $(line happy deploy-start) && $(line happy python-end) < $(line happy deploy-start) )) || fail 'deploy began before all branches finished'
grep -q '^pytest-end$' "$tmp/happy/events" || fail 'pytest not reached'
grep -q 'selected_suite_seconds=0' "$tmp/happy/output" || fail 'unlimited lifecycle did not disable pytest deadline'
(( $(line happy executable-check) < $(line happy pytest-end) )) || fail 'pytest ran before executable verification'
forward_pid=$(cat "$tmp/happy/artifacts/port-forward.log.pid")
for (( attempt=0; attempt<20; attempt++ )); do
  kill -0 "$forward_pid" 2>/dev/null || break
  sleep 0.1
done
! kill -0 "$forward_pid" 2>/dev/null || fail 'owned port-forward survived lifecycle completion'
echo 'PASS: lifecycle DAG waits, overlaps, and cleans its owned port-forward'
fixture branch-fail
started=$(date +%s)
run branch-fail 120
elapsed=$(($(date +%s)-started))
[[ $(cat "$tmp/branch-fail/status") == 23 ]] || { cat "$tmp/branch-fail/output" >&2; fail 'storage exit 23 not retained'; }
(( elapsed < 12 )) || fail "sibling cancellation took $elapsed seconds"
grep -q '^auth-term$' "$tmp/branch-fail/events" || fail 'auth sibling was not terminated'
grep -q '^kind-delete$' "$tmp/branch-fail/events" || fail 'owned partial cluster was not deleted'
! grep -q '^deploy-start$' "$tmp/branch-fail/events" || fail 'deploy ran after failure'
echo 'PASS: branch failure cancels sibling and cleans owned cluster'
fixture cache-fail
run cache-fail 120
[[ $(cat "$tmp/cache-fail/status") == 24 ]] || { cat "$tmp/cache-fail/output" >&2; fail 'late storage failure exit 24 not retained'; }
grep -q '^cache_ready=true$' "$tmp/cache-fail/github-output" || fail 'completed Python and Kind bootstrap did not save the warm cache after a later failure'
! grep -q '^deploy-start$' "$tmp/cache-fail/events" || fail 'late failure allowed deploy'
echo 'PASS: successful Python and Kind bootstrap saves cache before later setup failure'
fixture missing-marker
run missing-marker 120
[[ $(cat "$tmp/missing-marker/status") != 0 ]] || fail 'missing marker passed'
! grep -q '^deploy-start$' "$tmp/missing-marker/events" || fail 'missing marker allowed deploy'
grep -q '^kind-delete$' "$tmp/missing-marker/events" || fail 'missing-marker cluster was not deleted'
echo 'PASS: missing marker blocks deploy and cleans owned cluster'
set +e
python3 "$tmp/happy/run-command.py" "$tmp/happy" "$tmp/timeout.log" 1 bash -c 'sleep 10' >/dev/null 2>&1
status=$?
set -e
[[ $status == 124 ]] || fail "deadline returned $status, expected 124"
echo 'PASS: command deadline returns 124'

fixture wrong-java
cat >"$f/bin/java" <<'EOF'
#!/bin/sh
printf '%s\n' 'openjdk version "17.0.1"' >&2
EOF
run wrong-java 120
[[ $(cat "$tmp/wrong-java/status") == 2 ]] || fail 'unsupported Java did not fail preflight'
! grep -q '^build-start$' "$tmp/wrong-java/events" || fail 'build started without Java 21'
echo 'PASS: unsupported Java fails before any environment build'

fixture foreign-executable
run foreign-executable 120
[[ $(cat "$tmp/foreign-executable/status") == 73 ]] || fail 'foreign executable rejection was lost'
! grep -q '^pytest-end$' "$tmp/foreign-executable/events" || fail 'pytest ran with a foreign executable'
echo 'PASS: foreign executables block pytest'
