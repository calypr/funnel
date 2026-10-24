#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
workspace=$(cd "$script_dir/../.." && pwd)
export FUNNEL_CI_STATE_DIR=${FUNNEL_CI_STATE_DIR:-$workspace/.gen3-local}
mkdir -p "$FUNNEL_CI_STATE_DIR"
export PATH="$FUNNEL_CI_STATE_DIR/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
export GITHUB_WORKSPACE="$workspace"
export GITHUB_ENV="$FUNNEL_CI_STATE_DIR/environment"
export FUNNEL_CI_KUBECONFIG="$FUNNEL_CI_STATE_DIR/kubeconfig"
export KUBECONFIG="$FUNNEL_CI_KUBECONFIG"
export FUNNEL_CI_KIND_CLUSTER_NAME=${FUNNEL_CI_KIND_CLUSTER_NAME:-$(cat "$FUNNEL_CI_STATE_DIR/cluster-name" 2>/dev/null || printf funnel-gen3-local)}
export FUNNEL_CI_REUSE_CLUSTER=true
export FUNNEL_CI_GO_CACHE_DIR="$FUNNEL_CI_STATE_DIR/go-cache"
export FUNNEL_CI_FIXTURE_CACHE_DIR="$FUNNEL_CI_STATE_DIR/fixture-images"
export TEST_REPO="$FUNNEL_CI_STATE_DIR/checkouts/gen3-code-vigil"
export GEN3_GITOPS_REPO="$FUNNEL_CI_STATE_DIR/checkouts/gen3-gitops-ci"
export GITOPS_REPO="$GEN3_GITOPS_REPO"
export WORKING_DIR="${TEST_REPO}/gen3-integration-tests"
export NAMESPACE=gen3-local
export HOSTNAME=localhost:8000 HOSTNAME_PROTOCOL=http
export POETRY_VIRTUALENVS_PATH="$FUNNEL_CI_STATE_DIR/python/venvs"
export POETRY_CACHE_DIR="$FUNNEL_CI_STATE_DIR/python/cache"
export UV_CACHE_DIR="$FUNNEL_CI_STATE_DIR/python/uv"
export FUNNEL_CI_TEST_IMAGE_CACHE_DIR="$FUNNEL_CI_STATE_DIR/test-image-archives"
export NXF_HOME="$FUNNEL_CI_STATE_DIR/nextflow"
export NXF_VER=${NXF_VER:-26.10.1}
export AWS_DEFAULT_REGION=us-east-1 AWS_EC2_METADATA_DISABLED=true
export AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null
unset AWS_PROFILE AWS_DEFAULT_PROFILE AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
export SERVICE_TO_TEST=gen3_workflow
export GEN3_CHART_VERSION=0.3.110
export GEN3_VALUES_FILE="$script_dir/values-ci.yaml"
export FUNNEL_CI_BUILD_PLATFORM=${FUNNEL_CI_BUILD_PLATFORM:-linux/$(uname -m | sed 's/aarch64/arm64/;s/x86_64/amd64/')}
touch "$GITHUB_ENV"
while IFS='=' read -r key value; do
  if [[ "$key" =~ ^FUNNEL_CI_(IMAGE_[A-Z_]+|PLUGIN_[A-Z_]+|SOURCE_[A-Z_]+)$ ]]; then
    printf -v "$key" '%s' "$value"
    export "$key"
  fi
done <"$GITHUB_ENV"

usage() {
  cat <<'EOF'
Usage: bash tests/gen3/dev.sh <command> [args]

  ci                   Reset the owned fixture and run the complete CI lifecycle
  resume               Run the CI lifecycle against the retained owned fixture
  deps                 Prepare pinned checkouts and Python dependencies once
  build                Build current working files; create or reuse owned Kind
  step <phase>         Run only one bootstrap phase
                       values, render, images, tools, storage, install, database, auth, users, client, prepare, deploy, dpop
  test <pytest args>   Run a selected test against the existing environment
  forward              Keep the local HTTP port-forward in the foreground
  doctor               Check local cluster and Docker VM disk space
  status               Show existing pods without starting or rebuilding anything
  logs <resource>      Read logs, for example deployment/fence-deployment
  down                 Explicitly delete this checkout's owned cluster

State and logs persist in .gen3-local. The ci command resets only its owned
fixture at the start and retains it on failure; other commands preserve it. Run build only for image changes; step/test never build.
EOF
}

require_ownership_marker() {
  local expected
  expected=$(printf 'workspace=%s\ncluster=%s\nkubeconfig=%s\n' "$workspace" "$FUNNEL_CI_KIND_CLUSTER_NAME" "$KUBECONFIG")
  [[ -f "$FUNNEL_CI_STATE_DIR/kind-cluster-ownership" &&
    "$(cat "$FUNNEL_CI_STATE_DIR/kind-cluster-ownership")" == "$expected" ]] || {
    printf 'No matching owned cluster. Run build once, or select its existing state directory.\n' >&2
    return 2
  }
 }

require_owned_cluster() {
  require_ownership_marker
  local context cluster
  context=$(kubectl config current-context)
  cluster=$(kubectl config view --minify -o jsonpath='{.contexts[0].context.cluster}')
  [[ "$context" == "kind-$FUNNEL_CI_KIND_CLUSTER_NAME" && "$cluster" == "$context" ]] || {
    printf 'Explicit kubeconfig does not match the owned cluster.\n' >&2
    return 2
  }
  export FUNNEL_CI_CLUSTER_OWNED=true
}

check_capacity() {
  local available
  available=$(timeout 5 docker exec "$FUNNEL_CI_KIND_CLUSTER_NAME-worker" df -Pk /var | awk 'NR == 2 { print $4 }')
  [[ "$available" =~ ^[0-9]+$ ]] || { printf 'Cannot read Docker VM free space.\n' >&2; return 2; }
  if (( available < 2097152 )); then
    printf 'Docker VM has only %s KiB available; at least 2 GiB is required for reliable local tests. Free Docker VM space before retrying.\n' "$available" >&2
    return 2
  fi
  printf 'Docker VM available space: %s KiB\n' "$available"
}

checkout_pin() {
  local repo=$1 pin=$2 destination=$3
  if [[ ! -d "$destination/.git" ]]; then
    mkdir -p "$(dirname "$destination")"
    git clone --filter=blob:none --no-checkout "https://github.com/$repo.git" "$destination"
    git -C "$destination" checkout --detach "$pin"
  fi
  [[ "$(git -C "$destination" rev-parse HEAD)" == "$pin" ]] || {
    printf 'Checkout %s differs from pin %s; refusing to reset local changes.\n' "$destination" "$pin" >&2
    return 2
  }
}

command_name=${1:-help}
[[ $# == 0 ]] || shift
case "$command_name" in
  help|--help|-h) usage; exit 0 ;;
  ci|resume|deps|build|step|test|forward|doctor|status|logs|down) ;;
  *) usage >&2; exit 2 ;;
esac

run_dir=$(mktemp -d "$FUNNEL_CI_STATE_DIR/run-${command_name}-XXXXXX")
export FUNNEL_CI_ARTIFACTS_DIR="$run_dir"
started=$SECONDS
owned_pid=
forward_pid=
run_owned() {
  local status=0
  "$@" &
  owned_pid=$!
  wait "$owned_pid" || status=$?
  owned_pid=
  return "$status"
}
finish() {
  local status=$?
  trap - EXIT
  if [[ -n "$owned_pid" ]]; then
    kill -TERM "$owned_pid" 2>/dev/null || true
    wait "$owned_pid" 2>/dev/null || true
  fi
  if [[ -n "$forward_pid" ]]; then
    kill -TERM "$forward_pid" 2>/dev/null || true
  fi
  printf 'command=%s exit=%s elapsed_seconds=%s logs=%s\n' "$command_name" "$status" "$((SECONDS-started))" "$run_dir" | tee "$run_dir/result.txt"
}
trap finish EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

case "$command_name" in
  ci|resume)
    # Import the actual workflow's static contract; only runner-specific paths
    # and identities are supplied here.
    local_platform=$FUNNEL_CI_BUILD_PLATFORM
    while IFS=$'\t' read -r key value; do
      [[ "$key" =~ ^[A-Z][A-Z0-9_]*$ ]] || exit 2
      printf -v "$key" '%s' "$value"
      export "$key"
    done < <(yq -r '.jobs.integration_tests.env | to_entries[] | select(.value | contains("${{") | not) | [.key, .value] | @tsv' "$workspace/.github/workflows/integration_tests_on_kind.yaml")
    export FUNNEL_CI_BUILD_PLATFORM=$local_platform
    pinned_python="$FUNNEL_CI_STATE_DIR/python/runtimes/$GEN3_PYTHON_VERSION/python/bin/python3"
    if [[ ! -x "$pinned_python" ]]; then pinned_python=$(uv python find "$GEN3_PYTHON_VERSION"); fi
    export PATH="$(dirname "$pinned_python"):$PATH"
    for checkout in gen3-code-vigil gen3-gitops-ci; do
      destination="$workspace/$checkout"
      cached="$FUNNEL_CI_STATE_DIR/checkouts/$checkout"
      [[ -d "$cached/.git" ]] || { printf 'Missing pinned checkout; run dev.sh deps first\n' >&2; exit 2; }
      if [[ ! -e "$destination" && ! -L "$destination" ]]; then
        ln -s "$cached" "$destination"
      fi
      [[ "$(cd "$destination" && pwd -P)" == "$(cd "$cached" && pwd -P)" ]] || {
        printf 'Refusing to replace unrelated checkout %s\n' "$destination" >&2; exit 2;
      }
    done
    export TEST_REPO="$workspace/gen3-code-vigil"
    export GEN3_GITOPS_REPO="$workspace/gen3-gitops-ci" GITOPS_REPO="$workspace/gen3-gitops-ci"
    export WORKING_DIR=gen3-code-vigil/gen3-integration-tests
    export GEN3_VALUES_FILE="$script_dir/values-ci.yaml"
    export GH_WORKSPACE="$workspace" BRANCH=local-ci PR_NUM=local
    export COMMIT_SHA="$(git -C "$workspace" rev-parse HEAD)"
    export RUN_NUM=local RUN_ID=local ATTEMPT_NUM=1
    export FUNNEL_CI_ARTIFACTS_DIR="$run_dir"
    export FUNNEL_CI_KIND_READY_FILE="$FUNNEL_CI_STATE_DIR/kind-ready"
    export FUNNEL_CI_RETAIN_CLUSTER=true
    export GITHUB_OUTPUT="$run_dir/outputs"
    export FUNNEL_CI_STARTED_EPOCH=${FUNNEL_CI_STARTED_EPOCH:-$(date +%s)}
    export FUNNEL_CI_DEADLINE_EPOCH=${FUNNEL_CI_DEADLINE_EPOCH:-0}
    unset RUNNING_LOCAL
    for key in ${!FUNNEL_CI_IMAGE_@} ${!FUNNEL_CI_PLUGIN_@} ${!FUNNEL_CI_SOURCE_@}; do unset "$key"; done
    : >"$GITHUB_ENV"
    : >"$GITHUB_OUTPUT"
    bash "$script_dir/run-integration.sh" --preflight
    export FUNNEL_CI_FRESH_FIXTURE=true
    if [[ "$command_name" == resume ]]; then
      require_owned_cluster
      export FUNNEL_CI_FRESH_FIXTURE=false
    else
      if kind get clusters 2>/dev/null | awk -v cluster="$FUNNEL_CI_KIND_CLUSTER_NAME" '$0 == cluster { found=1 } END { exit !found }'; then
        require_owned_cluster
        printf 'Resetting owned fixture %s; dependency caches and logs are retained\n' "$FUNNEL_CI_KIND_CLUSTER_NAME"
        kind delete cluster --name "$FUNNEL_CI_KIND_CLUSTER_NAME"
      fi
      if [[ -f "$FUNNEL_CI_STATE_DIR/kind-cluster-ownership" ]]; then
        require_ownership_marker
        rm "$FUNNEL_CI_STATE_DIR/kind-cluster-ownership"
      fi
    fi
    rm -f "$FUNNEL_CI_KIND_READY_FILE"
    run_owned bash "$script_dir/run-integration.sh"
    ;;
  deps)
    checkout_pin uc-cdis/gen3-code-vigil 87da170a9a1cb099cfd1aded9402bc323201d6bc "$TEST_REPO"
    checkout_pin uc-cdis/gen3-gitops 19a687205de78d985416f44ad89c79bd2e66dab8 "$GEN3_GITOPS_REPO"
    python3 "$script_dir/configure-tests.py" "$WORKING_DIR"
    uv tool run --python 3.13 --from poetry==2.2.1 poetry -C "$WORKING_DIR" env use "$(uv python find 3.13)"
    uv tool run --python 3.13 --from poetry==2.2.1 poetry -C "$WORKING_DIR" install --only main --no-root --no-interaction
    ;;
  build)
    run_owned python3 "$script_dir/run-command.py" "$workspace" "$run_dir/build.log" 480 bash "$script_dir/build-and-start-kind.sh"
    ;;
  step)
    [[ $# == 1 ]] || { usage >&2; exit 2; }
    require_owned_cluster
    case "$1" in values|render) ;; *) check_capacity ;; esac
    run_owned python3 "$script_dir/run-command.py" "$workspace" "$run_dir/$1.log" "${GEN3_STEP_MAX_SECONDS:-240}" bash "$script_dir/bootstrap-services.sh" "$1"
    ;;
  test)
    [[ $# -gt 0 ]] || { printf 'Select a test node, -k expression, or --lf; use -m gen3_workflow for the suite.\n' >&2; exit 2; }
    require_owned_cluster
    check_capacity
    if ! curl --silent --output /dev/null --max-time 1 http://localhost:8000/; then
      bash "$script_dir/start-port-forward.sh" "$NAMESPACE" "$run_dir/port-forward.log"
      forward_pid=$(cat "$run_dir/port-forward.log.pid")
    fi
    mkdir -p "$WORKING_DIR/output" "$WORKING_DIR/logs" "$WORKING_DIR/allure-results"
    python3 "$script_dir/configure-tests.py" "$WORKING_DIR"
    run_owned bash "$script_dir/run-tests.sh" "$WORKING_DIR" "$run_dir" \
      uv tool run --python 3.13 --from poetry==2.2.1 poetry run pytest \
      -n 0 --maxfail=1 --max-worker-restart=0 -o timeout_method=signal -vv "$WORKING_DIR/tests" "$@"
    ;;
  forward)
    require_owned_cluster
    kubectl -n "$NAMESPACE" port-forward service/revproxy-service 8000:80
    ;;
  doctor)
    require_owned_cluster
    check_capacity
    ;;
  status)
    require_owned_cluster
    kubectl -n "$NAMESPACE" get pods,jobs -o wide
    ;;
  logs)
    [[ $# -gt 0 ]] || { usage >&2; exit 2; }
    require_owned_cluster
    kubectl -n "$NAMESPACE" logs "$@" --all-containers --tail=100
    ;;
  down)
    require_owned_cluster
    kind delete cluster --name "$FUNNEL_CI_KIND_CLUSTER_NAME"
    rm "$FUNNEL_CI_STATE_DIR/kind-cluster-ownership"
    ;;
esac
