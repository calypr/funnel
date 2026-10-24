#!/usr/bin/env bash
set -Eeuo pipefail

# Resumable bootstrap for the pinned Gen3 integration fixture. The Helm release
# is rendered and installed once with Jobs suspended and Deployments at zero;
# later commands provision PostgreSQL, activate prerequisites, sync users, and
# finally activate the already-rendered workload images.
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
workspace=$(cd "$script_dir/../.." && pwd)
state_dir=${FUNNEL_CI_STATE_DIR:-"$workspace/.gen3-local"}
test_repo=${TEST_REPO:-"$state_dir/checkouts/gen3-code-vigil"}
gitops_repo=${GEN3_GITOPS_REPO:-"$state_dir/checkouts/gen3-gitops-ci"}
namespace=${NAMESPACE:-gen3-local}
chart_version=${GEN3_CHART_VERSION:-0.3.110}
chart_archive="$state_dir/charts/gen3-${chart_version}.tgz"
values_file="$state_dir/gen3-values.yaml"
release_signature="$state_dir/gen3-release-values.sha256"
helm_bin=${HELM_BIN:-helm}
kubectl_bin=${KUBECTL_BIN:-kubectl}

: "${KUBECONFIG:?an explicit owned-cluster kubeconfig is required}"
: "${FUNNEL_CI_STATE_DIR:?persistent state directory is required}"
export KUBECONFIG
helm_root="$state_dir/helm${FUNNEL_CI_HELM_PROFILE:+/$FUNNEL_CI_HELM_PROFILE}"
mkdir -p "$state_dir/charts" "$helm_root/config" "$helm_root/cache" "$helm_root/data"
export HELM_CONFIG_HOME="$helm_root/config" HELM_CACHE_HOME="$helm_root/cache" HELM_DATA_HOME="$helm_root/data"

for command_name in "$helm_bin" "$kubectl_bin" yq; do
  command -v "$command_name" >/dev/null 2>&1 || { printf 'missing required command: %s\n' "$command_name" >&2; exit 2; }
done
[[ -d "$test_repo/.git" && -d "$gitops_repo/.git" ]] || {
  printf 'pinned Gen3 test and GitOps checkouts are required; run tests/gen3/dev.sh deps first\n' >&2
  exit 2
}
[[ "$(git -C "$test_repo" rev-parse HEAD)" == 87da170a9a1cb099cfd1aded9402bc323201d6bc ]] || {
  printf 'Gen3 test checkout is not the reviewed pin\n' >&2; exit 2;
}
[[ "$(git -C "$gitops_repo" rev-parse HEAD)" == 19a687205de78d985416f44ad89c79bd2e66dab8 ]] || {
  printf 'Gen3 GitOps checkout is not the reviewed pin\n' >&2; exit 2;
}

log() { printf '[gen3-bootstrap] %s\n' "$*"; }

ensure_values() {
  local source_values="$gitops_repo/ci/default/values"
  local input_file
  local -a merge_inputs
  [[ -f "$source_values/values.yaml" ]] || { printf 'missing pinned GitOps values at %s\n' "$source_values" >&2; return 2; }
  merge_inputs=("$source_values/values.yaml")
  for input_file in arborist.yaml fence.yaml funnel.yaml gen3-workflow.yaml indexd.yaml revproxy.yaml; do
    [[ -f "$source_values/$input_file" ]] || { printf 'missing selected GitOps values file: %s\n' "$input_file" >&2; return 2; }
    merge_inputs+=("$source_values/$input_file")
  done
  merge_inputs+=("$script_dir/values-ci.yaml")
  yq eval-all '. as $item ireduce ({}; . * $item)' "${merge_inputs[@]}" >"$values_file.tmp"
  mv "$values_file.tmp" "$values_file"

  USER_YAML_PATH="$script_dir/user.yaml" \
    yq eval -i '.fence.USER_YAML = load_str(strenv(USER_YAML_PATH))' "$values_file"
  GEN3_NAMESPACE="$namespace" MINIO_URL="http://minio.$namespace.svc.cluster.local:9000" yq eval -i \
    '.global.environment = strenv(GEN3_NAMESPACE) |
     .global.hostname = "localhost" |
     .funnel.Kubernetes.Namespace = strenv(GEN3_NAMESPACE) |
     .funnel.Kubernetes.JobsNamespace = "workflow-pods-" + strenv(GEN3_NAMESPACE) |
     .funnel.endpoint_url = strenv(MINIO_URL) |
     ."gen3-workflow".gen3WorkflowConfig.s3UpstreamEndpoint = strenv(MINIO_URL) |
     .fence.usersync.userYamlS3Path = "s3://cdis-gen3-users/ci/user.yaml" |
     .fence.usersync.userYamlS3Endpoint = strenv(MINIO_URL) |
     .fence.usersync.userYamlS3AccessKeyId = "minioadmin" |
     .fence.usersync.userYamlS3SecretAccessKey = "minioadmin" |
     .fence.FENCE_CONFIG.BASE_URL = "http://revproxy-service." + strenv(GEN3_NAMESPACE) + ".svc.cluster.local/user"' \
    "$values_file"
  log "selected values merged for pinned chart $chart_version"
}

ensure_chart() {
  [[ -f "$chart_archive" ]] && return 0
  "$helm_bin" repo add gen3 https://helm.gen3.org >/dev/null 2>&1 || true
  "$helm_bin" repo update gen3 >/dev/null
  "$helm_bin" pull gen3/gen3 --version "$chart_version" --destination "$state_dir/charts"
}

ensure_renderer_plugin() {
  if "$helm_bin" plugin list | awk '$1 == "funnel-ci-image" { found = 1 } END { exit !found }'; then
    "$helm_bin" plugin uninstall funnel-ci-image >/dev/null
  fi
  "$helm_bin" plugin install "$script_dir" >/dev/null
}

install_release_once() {
  ensure_values
  ensure_chart
  local expected existing release_status observed_values
  expected=$(shasum -a 256 "$values_file" | awk '{print $1}')
  if "$helm_bin" status "$namespace" -n "$namespace" >/dev/null 2>&1; then
    existing=$(cat "$release_signature" 2>/dev/null || true)
    release_status=$("$helm_bin" status "$namespace" -n "$namespace" -o json | jq -r '.info.status')
    if [[ "$existing" != "$expected" && -z "$existing" ]]; then
      observed_values=$("$helm_bin" get values "$namespace" -n "$namespace" -o json 2>/dev/null || printf '{}')
      if [[ "$(printf '%s' "$observed_values" | jq -S .)" == "$(yq -o=json "$values_file" | jq -S .)" ]]; then
        printf '%s\n' "$expected" >"$release_signature"
        existing=$expected
      fi
    fi
    if [[ "$existing" != "$expected" ]]; then
      printf 'existing Helm release uses different values. `dev.sh step render` previews the new values; an explicit refresh must preserve its generated Fence, OIDC, and PostgreSQL secrets. No cluster resources were deleted.\n' >&2
      return 2
    fi
    if [[ "$release_status" == deployed ]]; then
      log "reusing existing Helm release $namespace without a render or upgrade"
      return 0
    fi
    log "recovering previously failed Helm install $namespace in place"
    ensure_renderer_plugin
    FUNNEL_CI_BOOTSTRAP_RENDER=true "$helm_bin" upgrade "$namespace" "$chart_archive" \
      --namespace "$namespace" --values "$values_file" \
      --post-renderer funnel-ci-image --wait=hookOnly
    log "recovered Helm release $namespace without deleting its resources"
    return 0
  fi
  "$kubectl_bin" create namespace "$namespace" --dry-run=client -o yaml | "$kubectl_bin" apply -f - >/dev/null
  ensure_renderer_plugin
  FUNNEL_CI_BOOTSTRAP_RENDER=true "$helm_bin" install "$namespace" "$chart_archive" \
    --namespace "$namespace" --values "$values_file" \
    --post-renderer funnel-ci-image --wait=hookOnly
  printf '%s\n' "$expected" >"$release_signature"
  log "installed one stable Helm release; rendered Jobs are suspended and Deployments have zero replicas"
}

decode_base64() {
  if base64 --decode </dev/null >/dev/null 2>&1; then base64 --decode; else base64 -D; fi
}

secret_field() {
  local secret_name=$1 field=$2 encoded
  encoded=$("$kubectl_bin" -n "$namespace" get secret "$secret_name" -o "jsonpath={.data.$field}")
  [[ -n "$encoded" ]] || { printf 'secret %s is missing field %s\n' "$secret_name" "$field" >&2; return 1; }
  printf '%s' "$encoded" | decode_base64
}

provision_database() {
  local pg_pod service service_user service_pass service_db ready
  "$kubectl_bin" -n "$namespace" rollout status statefulset \
    -l "app.kubernetes.io/name=postgresql,app.kubernetes.io/instance=$namespace" \
    --timeout="${GEN3_DB_TIMEOUT:-180s}"
  pg_pod=$("$kubectl_bin" -n "$namespace" get pods \
    -l "app.kubernetes.io/name=postgresql,app.kubernetes.io/instance=$namespace" \
    -o jsonpath='{.items[0].metadata.name}')
  [[ -n "$pg_pod" ]] || { printf 'PostgreSQL pod not found\n' >&2; return 1; }
  for service in arborist fence indexd funnel; do
    service_user=$(secret_field "${service}-dbcreds" username)
    service_pass=$(secret_field "${service}-dbcreds" password)
    service_db=$(secret_field "${service}-dbcreds" database)
    "$kubectl_bin" -n "$namespace" exec -i "$pg_pod" -- env PGPASSWORD=gen3-ci-postgres \
      psql -h 127.0.0.1 -Upostgres -d postgres -v ON_ERROR_STOP=1 \
      -v "service_user=$service_user" -v "service_pass=$service_pass" \
      -v "service_db=$service_db" -f - <"$script_dir/provision-database.sql"
    "$kubectl_bin" -n "$namespace" exec -i "$pg_pod" -- env PGPASSWORD=gen3-ci-postgres \
      psql -h 127.0.0.1 -Upostgres -d "$service_db" -v ON_ERROR_STOP=1 \
      -v "service_user=$service_user" -f - <"$script_dir/provision-schema.sql"
    "$kubectl_bin" -n "$namespace" exec "$pg_pod" -- env PGPASSWORD="$service_pass" \
      psql -h 127.0.0.1 -U "$service_user" -d "$service_db" -v ON_ERROR_STOP=1 \
      -Atc 'SELECT current_user, current_database()'
    "$kubectl_bin" -n "$namespace" patch secret "${service}-dbcreds" --type merge \
      -p '{"data":{"dbcreated":"dHJ1ZQo="}}' >/dev/null
    ready=$(secret_field "${service}-dbcreds" dbcreated)
    [[ "$ready" == true ]] || { printf '%s dbcreated secret verification failed\n' "$service" >&2; return 1; }
    log "$service database and schema are ready; application login verified; dbcreated=true"
  done
}

wait_job_fail_fast() {
  local job_name=$1 timeout_seconds=${2:-120} elapsed=0 state document
  while (( elapsed < timeout_seconds )); do
    if ! document=$("$kubectl_bin" -n "$namespace" get job "$job_name" -o json 2>/dev/null); then
      printf 'Job %s disappeared while waiting\n' "$job_name" >&2
      return 1
    fi
    state=$(printf '%s' "$document" | jq -r '
      if any(.status.conditions[]?; .type == "Complete" and .status == "True") then "complete"
      elif any(.status.conditions[]?; .type == "Failed" and .status == "True") then "failed"
      else "running" end')
    if [[ "$state" == complete ]]; then return 0; fi
    if [[ "$state" == failed ]]; then
      "$kubectl_bin" -n "$namespace" describe job "$job_name" >&2 || true
      "$kubectl_bin" -n "$namespace" logs "job/$job_name" --all-containers --tail=100 >&2 || true
      printf 'Job %s failed; stopping instead of waiting through retries\n' "$job_name" >&2
      return 1
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  "$kubectl_bin" -n "$namespace" describe job "$job_name" >&2 || true
  "$kubectl_bin" -n "$namespace" logs "job/$job_name" --all-containers --tail=100 >&2 || true
  printf 'Job %s did not complete within %ss\n' "$job_name" "$timeout_seconds" >&2
  return 124
}

activate_auth() {
  local deployment
  if [[ -n "${FUNNEL_CI_AUTH_IMAGES_READY_FILE:-}" ]]; then
    log "waiting for authentication images before activating services"
    while [[ ! -f "$FUNNEL_CI_AUTH_IMAGES_READY_FILE" ]]; do sleep 1; done
  fi
  if [[ -n "${FUNNEL_CI_ARBORIST_READY_FILE:-}" ]]; then
    log "waiting for the verified native Arborist image"
    while [[ ! -f "$FUNNEL_CI_ARBORIST_READY_FILE" ]]; do sleep 1; done
  fi
  for deployment in arborist-deployment indexd-deployment fence-deployment presigned-url-fence-deployment; do
    "$kubectl_bin" -n "$namespace" get deployment "$deployment" >/dev/null 2>&1 || continue
    "$kubectl_bin" -n "$namespace" scale "deployment/$deployment" --replicas=1
  done
  for deployment in arborist-deployment indexd-deployment; do
    bash "$script_dir/wait-deployment-ready.sh" "$namespace" "$deployment" "${GEN3_AUTH_TIMEOUT_SECONDS:-120}"
  done
  if "$kubectl_bin" -n "$namespace" get job indexd-userdb >/dev/null 2>&1; then
    "$kubectl_bin" -n "$namespace" patch job indexd-userdb --type merge -p '{"spec":{"suspend":false}}' >/dev/null
    wait_job_fail_fast indexd-userdb "${GEN3_AUTH_TIMEOUT_SECONDS:-120}"
  fi
  for deployment in fence-deployment presigned-url-fence-deployment; do
    "$kubectl_bin" -n "$namespace" get deployment "$deployment" >/dev/null 2>&1 || continue
    bash "$script_dir/wait-deployment-ready.sh" "$namespace" "$deployment" "${GEN3_AUTH_TIMEOUT_SECONDS:-120}"
    log "$deployment is ready"
  done
}

create_funnel_client() {
  local pod client_output client_pair client_id client_secret
  pod=$("$kubectl_bin" -n "$namespace" get pods -l app=fence \
    -o jsonpath='{.items[?(@.status.containerStatuses[0].ready==true)].metadata.name}' | awk '{print $1}')
  [[ -n "$pod" ]] || { printf 'no ready Fence pod found\n' >&2; return 1; }
  "$kubectl_bin" -n "$namespace" exec "$pod" -- fence-create client-delete \
    --client funnel-plugin-client >/dev/null 2>&1 || true
  client_output=$("$kubectl_bin" -n "$namespace" exec "$pod" -- \
    fence-create client-create --client funnel-plugin-client --grant-types client_credentials)
  client_pair=$(printf '%s\n' "$client_output" | tail -n 1 | python3 -c \
    'import ast,sys; value=ast.literal_eval(sys.stdin.read().strip()); print(value[0]); print(value[1])')
  client_id=$(printf '%s\n' "$client_pair" | sed -n '1p')
  client_secret=$(printf '%s\n' "$client_pair" | sed -n '2p')
  [[ -n "$client_id" && -n "$client_secret" ]] || { printf 'Fence returned empty Funnel client credentials\n' >&2; return 1; }
  "$kubectl_bin" -n "$namespace" create secret generic funnel-oidc-client \
    --from-literal="client_id=$client_id" --from-literal="client_secret=$client_secret" \
    --dry-run=client -o yaml | "$kubectl_bin" -n "$namespace" apply -f - >/dev/null
  log 'Fence funnel-plugin-client created and credentials saved to funnel-oidc-client'
}

run_usersync() {
  "$kubectl_bin" -n "$namespace" delete job usersync-manual --ignore-not-found --wait=true --timeout=30s >/dev/null
  "$kubectl_bin" -n "$namespace" create job --from=cronjob/usersync usersync-manual
  wait_job_fail_fast usersync-manual "${GEN3_USERSYNC_TIMEOUT_SECONDS:-120}"
  "$kubectl_bin" -n "$namespace" logs job/usersync-manual --all-containers --tail=100
  log 'pinned upstream user.yaml synced into Fence and Arborist'
}

deploy_candidate() {
  local image plugin
  image=${FUNNEL_CI_IMAGE_REF:-}
  plugin=${FUNNEL_CI_PLUGIN_IMAGE_REF:-}
  : "${image:?build the candidate image before deploy}"
  : "${plugin:?build the Gen3 plugin image before deploy}"
  [[ "$image" =~ ^quay\.io/ohsu-comp-bio/funnel:(ci-[0-9a-f]{12}|local-[0-9a-f]{64})$ ]] || {
    printf 'unexpected candidate image reference: %s\n' "$image" >&2; return 2;
  }
  [[ "$plugin" =~ ^quay\.io/ohsu-comp-bio/funnel-gen3-plugin:(ci-[0-9a-f]{12}-eacaf197bfbc|local-[0-9a-f]{64}-eacaf197bfbc)$ ]] || {
    printf 'unexpected plugin image reference: %s\n' "$plugin" >&2; return 2;
  }
  "$kubectl_bin" -n "$namespace" set image deployment/funnel-server \
    "funnel=$image" "plugin=$plugin"
  "$kubectl_bin" -n "$namespace" scale deployment/gen3-workflow-deployment deployment/gen3-workflow-dpop-deployment --replicas=1
  "$kubectl_bin" -n "$namespace" scale deployment/funnel-server --replicas=1
  "$kubectl_bin" -n "$namespace" scale deployment/revproxy-deployment --replicas=1
  local deployment
  for deployment in gen3-workflow-deployment gen3-workflow-dpop-deployment funnel-server revproxy-deployment; do
    bash "$script_dir/wait-deployment-ready.sh" "$namespace" "$deployment" "${GEN3_DEPLOY_TIMEOUT_SECONDS:-180}"
  done
  log "candidate image activated in the existing Helm release: $image"
}

command_name=${1:-help}
case "$command_name" in
  values) ensure_values ;;
  render)
    ensure_values
    ensure_chart
    ensure_renderer_plugin
    render_path="$state_dir/render/gen3-bootstrap.yaml"
    mkdir -p "$(dirname "$render_path")"
    FUNNEL_CI_BOOTSTRAP_RENDER=true "$helm_bin" template "$namespace" "$chart_archive" \
      --namespace "$namespace" --values "$values_file" \
      --post-renderer funnel-ci-image --dry-run=server --take-ownership >"$render_path"
    postgres_count=$(NAMESPACE="$namespace" yq eval-all -r '[select(.kind == "StatefulSet" and .metadata.name == (strenv(NAMESPACE) + "-postgresql"))] | length' "$render_path")
    postgres_data_mounts=$(NAMESPACE="$namespace" yq eval-all -r '[select(.kind == "StatefulSet" and .metadata.name == (strenv(NAMESPACE) + "-postgresql")) | .spec.template.spec.containers[] | select(.name == "postgresql") | .volumeMounts[]? | select(.name == "data" and .mountPath == "/bitnami/postgresql")] | length' "$render_path")
    postgres_emptydir=$(NAMESPACE="$namespace" yq eval-all -r '[select(.kind == "StatefulSet" and .metadata.name == (strenv(NAMESPACE) + "-postgresql")) | .spec.template.spec.volumes[]? | select(.name == "data" and .emptyDir != null)] | length' "$render_path")
    external_secrets=$(yq eval-all -r '[select(.kind == "ExternalSecret" or .kind == "PushSecret")] | length' "$render_path")
    dbcreate_jobs=$(yq eval-all -r '[select(.kind == "Job") | select(.metadata.name | test("-dbcreate$"))] | length' "$render_path")
    oidc_jobs=$(yq eval-all -r '[select(.kind == "Job" and .metadata.name == "funnel-oidc-job")] | length' "$render_path")
    live_deployments=$(yq eval-all -r '[select(.kind == "Deployment") | select(.spec.replicas != 0)] | length' "$render_path")
    unsuspended_jobs=$(yq eval-all -r '[select(.kind == "Job") | select(.spec.suspend != true)] | length' "$render_path")
    unsuspended_schedules=$(yq eval-all -r '[select(.kind == "CronJob") | select(.spec.suspend != true)] | length' "$render_path")
    if [[ "$postgres_count" != 1 || "$postgres_data_mounts" != 1 || "$postgres_emptydir" != 1 || \
      "$external_secrets" != 0 || "$dbcreate_jobs" != 0 || \
      "$oidc_jobs" != 0 || "$live_deployments" != 0 || "$unsuspended_jobs" != 0 || "$unsuspended_schedules" != 0 ]]; then
      printf 'render assertions failed: PostgreSQL=%s data mounts=%s emptyDir=%s ExternalSecrets=%s DB-create jobs=%s OIDC jobs=%s live Deployments=%s active Jobs=%s active schedules=%s\n' \
        "$postgres_count" "$postgres_data_mounts" "$postgres_emptydir" "$external_secrets" "$dbcreate_jobs" "$oidc_jobs" "$live_deployments" "$unsuspended_jobs" "$unsuspended_schedules" >&2
      exit 1
    fi
    printf 'rendered Gen3 fixture to %s\n' "$render_path"
    ;;
  dpop)
    bash "$script_dir/bootstrap-services.sh" render
    dpop_render="$state_dir/render/dpop-live.yaml"
    NAMESPACE="$namespace" yq eval '
      select(.metadata.name == "gen3-workflow-dpop-deployment" or
             .metadata.name == "gen3-workflow-dpop-service" or
             .metadata.name == "gen3workflow-dpop-config" or
             .metadata.name == "funnel-from-dpop-gateway" or
             .metadata.name == "revproxy-nginx-subconf") |
      .metadata.namespace = (.metadata.namespace // strenv(NAMESPACE))
    ' "$state_dir/render/gen3-bootstrap.yaml" >"$dpop_render"
    "$kubectl_bin" apply -f "$dpop_render"
    "$kubectl_bin" -n "$namespace" scale deployment/gen3-workflow-dpop-deployment --replicas=1
    "$kubectl_bin" -n "$namespace" rollout restart deployment/revproxy-deployment
    for deployment in gen3-workflow-dpop-deployment revproxy-deployment; do
      bash "$script_dir/wait-deployment-ready.sh" "$namespace" "$deployment" "${GEN3_DEPLOY_TIMEOUT_SECONDS:-120}"
    done
    ;;
  images) python3 "$script_dir/cache-fixture-images.py" seed ;;
  tools) bash "$script_dir/prepare-test-tools.sh" ;;
  storage) "$script_dir/setup-environment.sh" ;;
  install) install_release_once ;;
  database) provision_database ;;
  auth) activate_auth ;;
  client) create_funnel_client ;;
  users) run_usersync ;;
  prepare-auth)
    install_release_once
    provision_database
    activate_auth
    create_funnel_client
    ;;
  prepare)
    ensure_values
    "$script_dir/setup-environment.sh"
    install_release_once
    provision_database
    activate_auth
    create_funnel_client
    run_usersync
    ;;
  deploy) deploy_candidate ;;
  *)
    printf 'usage: %s {values|render|images|tools|storage|install|database|auth|client|users|prepare|deploy|dpop}\n' "$0" >&2
    exit 2
    ;;
esac
