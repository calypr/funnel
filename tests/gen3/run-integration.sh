#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
workspace=${GITHUB_WORKSPACE:?GitHub workspace path is required}
test_repo=${TEST_REPO:?pinned Gen3 test checkout is required}
test_repo_sha=${TEST_REPO_SHA:?pinned Gen3 test commit is required}
test_root="$workspace/${WORKING_DIR:?Gen3 test working directory is required}"
gitops_repo=${GEN3_GITOPS_REPO:?pinned Gen3 GitOps checkout is required}
gitops_sha=${GEN3_GITOPS_SHA:?pinned Gen3 GitOps commit is required}
artifacts_dir=${FUNNEL_CI_ARTIFACTS_DIR:?artifact directory is required}
timings_file="$artifacts_dir/timings.tsv"
lifecycle_log="$artifacts_dir/lifecycle.log"
bootstrap_log="$artifacts_dir/python-bootstrap.log"
bootstrap_status_file="$artifacts_dir/python-bootstrap.exit"
build_log="$artifacts_dir/build-kind.log"
build_status_file="$artifacts_dir/build-kind.exit"
declare -A phase_pid=()
declare -A phase_status=()
cache_ready=0
phase_names=()
test_runner_started=0
exit_status=0

mkdir -p "$artifacts_dir"

record_event() {
  printf '%s\t%s\t%s\t%s\n' \
    "$1" "$2" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${3:--}" >>"$timings_file"
}

log() {
  printf '[gen3-ci] %s\n' "$*" | tee -a "$lifecycle_log"
}

remaining_seconds() {
  local now
  now=$(date +%s)
  printf '%s\n' "$((FUNNEL_CI_DEADLINE_EPOCH - now))"
}

run_phase() {
  local name=$1
  shift
  local phase_limit status
  phase_limit=$(phase_limit_seconds) || {
    log "not enough lifecycle budget to start phase: $name"
    return 124
  }

  record_event "$name" start
  log "phase start: $name"
  set +e
  python3 "$script_dir/run-command.py" "$workspace" "$artifacts_dir/$name.log" \
    "$phase_limit" "$@" 2>&1 | tee -a "$lifecycle_log"
  local -a pipeline_status=("${PIPESTATUS[@]}")
  set -e
  status=${pipeline_status[0]}
  record_event "$name" end "$status"
  log "phase end: $name exit=$status"
  return "$status"
}

run_test_phase() {
  local remaining test_limit outer_limit status
  test_limit=$(phase_limit_seconds) || {
    log 'not enough lifecycle budget to start pytest while preserving final diagnostics time'
    return 124
  }
  outer_limit=0
  if (( FUNNEL_CI_DEADLINE_EPOCH != 0 )); then
    remaining=$(remaining_seconds)
    outer_limit=$remaining
  fi

  record_event pytest start
  log "phase start: pytest selected_suite_seconds=$test_limit"
  test_runner_started=1
  set +e
  GEN3_TEST_MAX_SECONDS="$test_limit" \
    python3 "$script_dir/run-command.py" "$workspace" "$artifacts_dir/pytest-wrapper.log" \
      "$outer_limit" bash "$script_dir/run-tests.sh" "$test_root" "$artifacts_dir" \
        uv tool run --from poetry==2.2.1 poetry run pytest "$test_root/tests" \
        --video=retain-on-failure \
        -n "${FUNNEL_CI_PYTEST_WORKERS:-4}" \
        --maxfail=1 --max-worker-restart=0 \
        -m 'gen3_workflow and not wip' \
        --alluredir "$test_root/allure-results" \
        --no-header \
        --dist loadscope \
        -o faulthandler_timeout=240 \
        -o timeout_method=signal \
        -vv 2>&1 | tee -a "$lifecycle_log"
  local -a pipeline_status=("${PIPESTATUS[@]}")
  set -e
  status=${pipeline_status[0]}
  record_event pytest end "$status"
  log "phase end: pytest exit=$status"
  return "$status"
}

load_hook_environment() {
  local key value
  [[ -f "$GITHUB_ENV" ]] || return 0
  while IFS='=' read -r key value; do
    case "$key" in
      FUNNEL_CI_[A-Z0-9_]*|SKIP_QUAY_BUILD|PYTEST_ADDOPTS|HOSTNAME|HOSTNAME_PROTOCOL)
        [[ "$key" =~ ^[A-Z][A-Z0-9_]*$ ]] || continue
        [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || {
          printf 'multiline hook output is not supported for %s\n' "$key" >&2
          return 2
        }
        printf -v "$key" '%s' "$value"
        export "$key"
        ;;
    esac
  done <"$GITHUB_ENV"
  if [[ -n "${FUNNEL_CI_KUBECONFIG:-}" ]]; then
    KUBECONFIG=$FUNNEL_CI_KUBECONFIG
    export KUBECONFIG
  fi
}

phase_limit_seconds() {
  if (( FUNNEL_CI_DEADLINE_EPOCH == 0 )); then
    printf '0\n'
    return 0
  fi
  local remaining limit
  remaining=$(remaining_seconds)
  limit=$((remaining - 60))
  if (( limit < 1 )); then
    return 124
  fi
  printf '%s\n' "$limit"
}

start_parallel_phase() {
  local name=$1 log_path=$2 status_path=$3
  shift 3
  local limit
  limit=$(phase_limit_seconds) || {
    log "not enough lifecycle budget to start phase: $name"
    return 124
  }
  rm -f "$status_path"
  record_event "$name" start
  log "phase start: $name"
  phase_names+=("$name")
  python3 "$script_dir/run-command.py" "$workspace" "$log_path" "$limit" "$@" \
    2>&1 &
  phase_pid["$name"]=$!
}

finish_parallel_phase() {
  local name=$1 status=$2
  phase_status[$name]=$status
  if (( cache_ready == 0 )) && [[ "${phase_status[python-dependencies]:-}" == 0 && "${phase_status[build-kind]:-}" == 0 ]]; then
    printf 'cache_ready=true\n' >>"$GITHUB_OUTPUT"
    cache_ready=1
  fi
  record_event "$name" end "$status"
  log "phase end: $name exit=$status"
  case "$name" in
    python-dependencies) printf '%s\n' "$status" >"$bootstrap_status_file" ;;
    build-kind) printf '%s\n' "$status" >"$build_status_file" ;;
  esac
  return 0
}

stop_parallel_phases() {
  local name pid status
  for name in "${phase_names[@]}"; do
    pid=${phase_pid[$name]:-}
    [[ -n "$pid" ]] || continue
    kill -TERM "$pid" 2>/dev/null || true
  done
  for name in "${phase_names[@]}"; do
    pid=${phase_pid[$name]:-}
    [[ -n "$pid" ]] || continue
    set +e
    wait "$pid"
    status=$?
    set -e
    finish_parallel_phase "$name" "$status"
    unset 'phase_pid[$name]'
  done
}

reap_finished_phases() {
  local running name pid status
  running=$(jobs -pr || true)
  for name in "${phase_names[@]}"; do
    pid=${phase_pid[$name]:-}
    [[ -n "$pid" ]] || continue
    if ! printf '%s\n' "$running" | awk -v pid="$pid" '$0 == pid { found = 1 } END { exit !found }'; then
      set +e
      wait "$pid"
      status=$?
      set -e
      finish_parallel_phase "$name" "$status"
      unset 'phase_pid[$name]'
      if (( status != 0 )); then
        stop_parallel_phases
        return "$status"
      fi
    fi
  done
}

wait_for_kind_ready() {
  local ready_file=${FUNNEL_CI_KIND_READY_FILE:?Kind ready marker path is required}
  local kubeconfig=${FUNNEL_CI_KUBECONFIG:?explicit kubeconfig path is required}
  local state_dir=${FUNNEL_CI_STATE_DIR:?persistent state directory is required}
  [[ "$ready_file" == "$state_dir/kind-ready" && "$kubeconfig" == "$state_dir/kubeconfig" ]] || {
    log 'Kind marker and kubeconfig must belong to the explicit lifecycle state directory'
    return 2
  }
  log "waiting for Kind readiness marker: $ready_file"
  while [[ ! -e "$ready_file" ]]; do
    reap_finished_phases || return $?
    ((${#phase_pid[@]})) || { log 'all startup branches ended before Kind reported readiness'; return 1; }
    sleep 1
  done
  load_hook_environment
  [[ -f "$kubeconfig" ]] || { log "ready marker exists but kubeconfig is missing: $kubeconfig"; return 2; }
  export KUBECONFIG="$kubeconfig"
  while ! timeout --signal=TERM --kill-after=1s 6s kubectl --kubeconfig "$kubeconfig" get --raw=/readyz >/dev/null 2>&1; do
    reap_finished_phases || return $?
    ((${#phase_pid[@]})) || { log 'startup branches ended while the explicit Kind API remained unavailable'; return 1; }
    sleep 1
  done
  log 'Kind marker is present and the explicit cluster API /readyz responds'
}

wait_for_parallel_phases() {
  while ((${#phase_pid[@]})); do
    reap_finished_phases || return $?
    ((${#phase_pid[@]} == 0)) || sleep 1
  done
  return 0
}

wait_for_named_phases() {
  local name
  while :; do
    local pending=0
    for name in "$@"; do
      [[ -n "${phase_pid[$name]:-}" ]] && pending=1
    done
    (( pending == 1 )) || return 0
    reap_finished_phases || return $?
    sleep 1
  done
}

preflight() {
  local command_name variable_name
  local required_environment=(
    REPO REPO_FN BRANCH PR_NUM COMMIT_SHA RUN_NUM RUN_ID ATTEMPT_NUM GH_WORKSPACE
    CI_ENV HELM_BRANCH QUAY_ORG QUAY_REPO EXTERNAL_TO_CTDS HOSTNAME HOSTNAME_PROTOCOL
  )
  for command_name in awk aws bash curl docker git helm java jq kind kubectl python python3 timeout tee uv yq; do
    command -v "$command_name" >/dev/null 2>&1 || {
      printf 'missing required command: %s\n' "$command_name" >&2
      return 2
    }
  done
  local java_version
  java_version=$(java -version 2>&1) || { printf 'Java 21 is required by Nextflow\n' >&2; return 2; }
  [[ "$java_version" == *'version "21.'* ]] || {
    printf 'Java 21 is required by Nextflow; found: %s\n' "$java_version" >&2
    return 2
  }
  for variable_name in "${required_environment[@]}"; do
    [[ -n "${!variable_name:-}" ]] || {
      printf 'required workflow input is empty: %s\n' "$variable_name" >&2
      return 2
    }
  done
  [[ "$GH_WORKSPACE" == "$workspace" && "$test_repo" == "$workspace/gen3-code-vigil" && \
    "$test_root" == "$test_repo/gen3-integration-tests" && \
    "$gitops_repo" == "$workspace/gen3-gitops-ci" ]] || {
    printf 'workspace, pinned checkout, or test-root paths do not match the workflow contract\n' >&2
    return 2
  }
  [[ "$FUNNEL_CI_ARTIFACTS_DIR" == /* ]] || {
    printf 'FUNNEL_CI_ARTIFACTS_DIR must be absolute\n' >&2
    return 2
  }
  [[ "$FUNNEL_CI_STATE_DIR" == /* && \
    "$FUNNEL_CI_KUBECONFIG" == "$FUNNEL_CI_STATE_DIR/kubeconfig" && \
    "$FUNNEL_CI_KIND_READY_FILE" == "$FUNNEL_CI_STATE_DIR/kind-ready" ]] || {
    printf 'Kind state, kubeconfig, and ready marker must use the explicit lifecycle state directory\n' >&2
    return 2
  }
  [[ "$FUNNEL_CI_BUILD_PLATFORM" =~ ^linux/(amd64|arm64)$ ]] || {
    printf 'unsupported FUNNEL_CI_BUILD_PLATFORM: %s\n' "$FUNNEL_CI_BUILD_PLATFORM" >&2
    return 2
  }
  [[ "$SERVICE_TO_TEST" == gen3_workflow ]] || {
    printf 'SERVICE_TO_TEST must remain gen3_workflow\n' >&2
    return 2
  }
  [[ "$REPO" == funnel && "$REPO_FN" == calypr/funnel && "$CI_ENV" == default && \
    "$HELM_BRANCH" == master && "$EXTERNAL_TO_CTDS" == true && \
    "$QUAY_ORG" == ohsu-comp-bio && "$QUAY_REPO" == funnel && \
    "$HOSTNAME" == localhost:8000 && "$HOSTNAME_PROTOCOL" == http ]] || {
    printf 'upstream Gen3 environment values differ from the owned workflow contract\n' >&2
    return 2
  }
  [[ "$test_repo_sha" == 87da170a9a1cb099cfd1aded9402bc323201d6bc ]] || {
    printf 'unexpected Gen3 test repository pin: %s\n' "$test_repo_sha" >&2
    return 2
  }
  [[ "$gitops_sha" == 19a687205de78d985416f44ad89c79bd2e66dab8 ]] || {
    printf 'unexpected Gen3 GitOps repository pin: %s\n' "$gitops_sha" >&2
    return 2
  }
  [[ "$WORKING_DIR" == gen3-code-vigil/gen3-integration-tests ]] || {
    printf 'unexpected Gen3 WORKING_DIR: %s\n' "$WORKING_DIR" >&2
    return 2
  }
  [[ "$GEN3_CHART_VERSION" == 0.3.110 && \
    "$GEN3_VALUES_FILE" == "$workspace/tests/gen3/values-ci.yaml" ]] || {
    printf 'unexpected Gen3 chart or fixture values input\n' >&2
    return 2
  }
  [[ -f "$test_root/pyproject.toml" && -f "$test_root/poetry.lock" ]] || {
    printf 'pinned Gen3 test checkout must include pyproject.toml and poetry.lock\n' >&2
    return 2
  }
  [[ -f "$script_dir/run-command.py" && -f "$script_dir/redact_logs.py" && \
    -f "$script_dir/configure-tests.py" ]] || {
    printf 'bounded lifecycle or test configuration helper is missing\n' >&2
    return 2
  }
  [[ -f "$gitops_repo/ci/default/values/values.yaml" ]] || {
    printf 'pinned Gen3 GitOps values are missing\n' >&2
    return 2
  }
  [[ -f "$GEN3_VALUES_FILE" ]] || {
    printf 'Gen3 fixture values file is missing: %s\n' "$GEN3_VALUES_FILE" >&2
    return 2
  }
  [[ -n "${GITHUB_ENV:-}" && -f "$GITHUB_ENV" ]] || {
    printf 'GITHUB_ENV must be provided by GitHub Actions\n' >&2
    return 2
  }
  [[ -n "${GITHUB_OUTPUT:-}" && -f "$GITHUB_OUTPUT" ]] || {
    printf 'GITHUB_OUTPUT must be provided by GitHub Actions\n' >&2
    return 2
  }
  [[ "$NAMESPACE" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || {
    printf 'invalid Kubernetes namespace: %s\n' "$NAMESPACE" >&2
    return 2
  }
  [[ "$FUNNEL_CI_DEADLINE_EPOCH" =~ ^[0-9]+$ ]] || {
    printf 'FUNNEL_CI_DEADLINE_EPOCH must be an integer epoch time\n' >&2
    return 2
  }
  [[ "$(git -C "$test_repo" rev-parse HEAD)" == "$test_repo_sha" ]] || {
    printf 'Gen3 test checkout does not match its pinned commit\n' >&2
    return 2
  }
  [[ "$(git -C "$gitops_repo" rev-parse HEAD)" == "$gitops_sha" ]] || {
    printf 'Gen3 GitOps checkout does not match its pinned commit\n' >&2
    return 2
  }
  [[ "$(python --version 2>&1)" == Python\ 3.13.* ]] || {
    printf 'Gen3 integration tests require Python 3.13\n' >&2
    return 2
  }
  if [[ -n "${GEN3_PYTHON_VERSION:-}" && "$(python --version 2>&1)" != "Python $GEN3_PYTHON_VERSION" ]]; then
    printf 'Python patch version differs from the shared workflow pin %s\n' "$GEN3_PYTHON_VERSION" >&2
    return 2
  fi
  [[ "$(helm version --template '{{.Version}}')" == v4.3.1 ]] || {
    printf 'Gen3 integration tests require Helm v4.3.1\n' >&2
    return 2
  }
  timeout --signal=TERM --kill-after=2s 10s docker info >/dev/null
}

if [[ "${1:-}" == --preflight ]]; then
  preflight
  exit $?
fi

capture_cluster_diagnostics() {
  local output="$artifacts_dir/setup-failure-diagnostics.log"
  [[ -n "${KUBECONFIG:-}" && -f "$KUBECONFIG" ]] || return 0
  {
    printf '%s\n' '--- nodes ---'
    timeout --signal=TERM --kill-after=1s 4s kubectl get nodes -o wide || true
    printf '%s\n' '--- namespace pods ---'
    timeout --signal=TERM --kill-after=1s 4s kubectl get pods -n "$NAMESPACE" -o wide || true
    printf '%s\n' '--- namespace events ---'
    timeout --signal=TERM --kill-after=1s 4s kubectl get events -n "$NAMESPACE" --sort-by=.lastTimestamp || true
    printf '%s\n' '--- kube-system pods ---'
    timeout --signal=TERM --kill-after=1s 4s kubectl get pods -n kube-system -o wide || true
    printf '%s\n' '--- S3 CSI workloads ---'
    timeout --signal=TERM --kill-after=1s 4s kubectl -n kube-system describe daemonset/s3-csi-node deployment/s3-csi-controller || true
    printf '%s\n' '--- kube-system events ---'
    timeout --signal=TERM --kill-after=1s 4s kubectl get events -n kube-system --sort-by=.lastTimestamp || true
    if [[ "$exit_status" != 0 && -f "$script_dir/collect-task-diagnostics.sh" ]]; then
      timeout --signal=TERM --kill-after=2s 18s bash "$script_dir/collect-task-diagnostics.sh" snapshot "$NAMESPACE" || true
    fi
  } >>"$output" 2>&1
}

finish() {
  exit_status=$?
  trap - EXIT
  stop_parallel_phases
  local forward_pid_file="$artifacts_dir/port-forward.log.pid" forward_pid
  if [[ -f "$forward_pid_file" ]]; then
    read -r forward_pid <"$forward_pid_file"
    if [[ "$forward_pid" =~ ^[0-9]+$ ]]; then kill -TERM "$forward_pid" 2>/dev/null || true; fi
  fi
  load_hook_environment || true
  if (( exit_status != 0 )); then
    if (( test_runner_started == 0 )) && [[ "${FUNNEL_CI_CLUSTER_OWNED:-}" == true ]]; then
      capture_cluster_diagnostics
    fi
  fi
  if [[ "${FUNNEL_CI_RETAIN_CLUSTER:-false}" == true && "${FUNNEL_CI_REUSE_CLUSTER:-false}" == true ]]; then
    log "retaining the owned local fixture for diagnosis: ${FUNNEL_CI_KIND_CLUSTER_NAME}"
  elif [[ "${FUNNEL_CI_CLUSTER_OWNED:-}" == true ]] && command -v kind >/dev/null 2>&1; then
    timeout --signal=TERM --kill-after=3s 20s kind delete cluster \
      --name "${FUNNEL_CI_KIND_CLUSTER_NAME:-kind-cluster}" \
      >>"$lifecycle_log" 2>&1 || true
  fi
  local elapsed
  elapsed=$(($(date +%s) - ${FUNNEL_CI_STARTED_EPOCH:-$(date +%s)}))
  record_event lifecycle end "$exit_status"
  log "lifecycle finished exit=$exit_status elapsed_seconds=$elapsed"
  exit "$exit_status"
}

trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

[[ "$FUNNEL_CI_STARTED_EPOCH" =~ ^[0-9]+$ ]] || {
  printf 'FUNNEL_CI_STARTED_EPOCH must be set by the first workflow step\n' >&2
  exit 2
}
record_event lifecycle start
printf 'started_epoch\t%s\ndeadline_epoch\t%s\n' \
  "$FUNNEL_CI_STARTED_EPOCH" "$FUNNEL_CI_DEADLINE_EPOCH" >"$artifacts_dir/run-window.tsv"

run_phase preflight bash "$script_dir/run-integration.sh" --preflight || exit $?
run_phase environment bash "$script_dir/describe-environment.sh" || exit $?
run_phase configure-tests python3 "$script_dir/configure-tests.py" "$test_root" || exit $?
mkdir -p "$HOME/.gen3" "$test_root/logs" "$test_root/output" "$test_root/allure-results"

start_parallel_phase python-dependencies "$bootstrap_log" "$bootstrap_status_file" \
  bash "$script_dir/prepare-python.sh" "$test_root" || exit $?
start_parallel_phase build-kind "$build_log" "$build_status_file" \
  bash "$script_dir/build-and-start-kind.sh" || exit $?
wait_for_kind_ready || { status=$?; stop_parallel_phases; exit "$status"; }
load_hook_environment
[[ -n "${KUBECONFIG:-}" && -f "$KUBECONFIG" ]] || {
  printf 'workflow did not provide the explicit Kind kubeconfig\n' >&2
  exit 2
}

export FUNNEL_CI_ARBORIST_READY_FILE="$artifacts_dir/arborist-ready"
start_parallel_phase test-tools "$artifacts_dir/test-tools.log" "$artifacts_dir/test-tools.exit" \
  bash "$script_dir/prepare-test-tools.sh" || exit $?
start_parallel_phase gen3-storage "$artifacts_dir/gen3-storage.log" "$artifacts_dir/gen3-storage.exit" \
  env FUNNEL_CI_HELM_PROFILE=storage bash "$script_dir/setup-environment.sh" || exit $?
start_parallel_phase gen3-auth "$artifacts_dir/gen3-auth.log" "$artifacts_dir/gen3-auth.exit" \
  env FUNNEL_CI_HELM_PROFILE=bootstrap-auth bash "$script_dir/bootstrap-services.sh" prepare-auth || exit $?
wait_for_named_phases gen3-storage gen3-auth test-tools || { status=$?; stop_parallel_phases; exit "$status"; }
load_hook_environment
start_parallel_phase gen3-users "$artifacts_dir/gen3-users.log" "$artifacts_dir/gen3-users.exit" \
  env FUNNEL_CI_HELM_PROFILE=bootstrap-users bash "$script_dir/bootstrap-services.sh" users || exit $?
wait_for_parallel_phases || exit $?
load_hook_environment

run_phase deploy-gen3 env FUNNEL_CI_HELM_PROFILE=bootstrap-deploy \
  bash "$script_dir/bootstrap-services.sh" deploy || exit $?
load_hook_environment

run_phase expose-gen3 bash "$script_dir/expose-and-verify-environment.sh" || exit $?
load_hook_environment
export PYTEST_ADDOPTS=${PYTEST_ADDOPTS:-}

run_phase executable-architecture bash "$script_dir/verify-service-executables.sh" || exit $?
run_phase cache-fixtures python3 "$script_dir/cache-fixture-images.py" save || exit $?
run_test_phase || exit $?
