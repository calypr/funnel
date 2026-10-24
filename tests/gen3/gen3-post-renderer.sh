#!/usr/bin/env bash
set -Eeuo pipefail

bootstrap_render=${FUNNEL_CI_BOOTSTRAP_RENDER:-false}
if [[ "$bootstrap_render" == true ]]; then
  workspace=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
  source_sha=${FUNNEL_CI_SOURCE_SHA:-${COMMIT_SHA:-$(git -C "$workspace" rev-parse HEAD)}}
  FUNNEL_CI_IMAGE_REF=${FUNNEL_CI_IMAGE_REF:-quay.io/ohsu-comp-bio/funnel:ci-${source_sha:0:12}}
  FUNNEL_CI_PLUGIN_IMAGE_REF=${FUNNEL_CI_PLUGIN_IMAGE_REF:-quay.io/ohsu-comp-bio/funnel-gen3-plugin:ci-${source_sha:0:12}-eacaf197bfbc}
fi
: "${FUNNEL_CI_IMAGE_REF:?candidate image reference is required}"
FUNNEL_CI_PLUGIN_IMAGE_REF=${FUNNEL_CI_PLUGIN_IMAGE_REF:-}

if [[ ! "$FUNNEL_CI_IMAGE_REF" =~ ^quay\.io/ohsu-comp-bio/funnel:(ci-[0-9a-f]{12}|local-[0-9a-f]{64})$ ]]; then
  printf 'unexpected candidate image reference: %s\n' "$FUNNEL_CI_IMAGE_REF" >&2
  exit 1
fi
if [[ -n "$FUNNEL_CI_PLUGIN_IMAGE_REF" && ! "$FUNNEL_CI_PLUGIN_IMAGE_REF" =~ ^quay\.io/ohsu-comp-bio/funnel-gen3-plugin:(ci-[0-9a-f]{12}-eacaf197bfbc|local-[0-9a-f]{64}-eacaf197bfbc)$ ]]; then
  printf 'unexpected plugin image reference: %s\n' "$FUNNEL_CI_PLUGIN_IMAGE_REF" >&2
  exit 1
fi
export FUNNEL_CI_IMAGE_REF FUNNEL_CI_PLUGIN_IMAGE_REF
fixture_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
FUNNEL_CI_AWSHELPER_IMAGE=$(yq -r ".global.awshelper_container_image" "$fixture_dir/values-ci.yaml")
export FUNNEL_CI_AWSHELPER_IMAGE
export FUNNEL_CI_NATIVE_AWSHELPER_IMAGE=funnel-gen3-awshelper:ci

render_dir=$(mktemp -d)
trap 'rm -rf "$render_dir"' EXIT
input="$render_dir/input.yaml"
output="$render_dir/output.yaml"
cat >"$input"

deployment_count=$(yq eval-all -r '[select(.kind == "Deployment" and .metadata.name == "funnel-server")] | length' "$input")
configmap_count=$(yq eval-all -r '[select(.kind == "ConfigMap" and .metadata.name == "funnel-server-config")] | length' "$input")
if [[ "$deployment_count" != 1 || "$configmap_count" != 1 ]]; then
  printf 'expected one Funnel Deployment and config map, got %s and %s\n' "$deployment_count" "$configmap_count" >&2
  exit 1
fi
# Native task fixtures must never be replaced by the upstream x86-only tags.
export FUNNEL_CI_EXECUTOR_PULL_ORIGINAL='imagePullPolicy: {{if .ImagePullPolicy}}{{.ImagePullPolicy}}{{else}}Always{{end}}'
export FUNNEL_CI_EXECUTOR_PULL_NATIVE='imagePullPolicy: {{if or (eq .Image "quay.io/cdis/gen3-workflow:integration_tests_dicom_image") (eq .Image "quay.io/biocontainers/ubuntu:24.04")}}Never{{else}}{{if .ImagePullPolicy}}{{.ImagePullPolicy}}{{else}}Always{{end}}{{end}}'
worker_template=$(yq eval-all -r 'select(.kind == "ConfigMap" and .metadata.name == "funnel-server-config") | .data."funnel-server.yaml" | from_yaml | .Kubernetes.WorkerTemplate' "$input")
if [[ $(printf '%s\n' "$worker_template" | awk '/imagePullPolicy: Always$/ { count++ } END { print count + 0 }') != 1 || "$worker_template" != *'image: {{.Image}}'* ]]; then
  printf 'unexpected Funnel worker template image contract\n' >&2
  exit 1
fi

FUNNEL_CI_IMAGE_REF="$FUNNEL_CI_IMAGE_REF" FUNNEL_CI_PLUGIN_IMAGE_REF="$FUNNEL_CI_PLUGIN_IMAGE_REF" \
  yq eval '
    (select(.kind == "Deployment" and .metadata.name == "funnel-server") |
      (.spec.template.spec.containers[] | select(.name == "funnel")) |=
        (.image = strenv(FUNNEL_CI_IMAGE_REF) | .imagePullPolicy = "Never")
    ) //
    (select(.kind == "ConfigMap" and .metadata.name == "funnel-server-config") |
      .data."funnel-server.yaml" |= (
        from_yaml |
        .Kubernetes.WorkerTemplate |= sub("imagePullPolicy: Always"; "imagePullPolicy: Never") |
        to_yaml
      )
    ) // .
  ' "$input" >"$output"
if [[ -n "$FUNNEL_CI_PLUGIN_IMAGE_REF" ]]; then
  yq eval '(select(.kind == "ConfigMap" and .metadata.name == "funnel-server-config") |
    .data."funnel-server.yaml" |= (from_yaml |
      .Kubernetes.ExecutorTemplate |= sub(strenv(FUNNEL_CI_EXECUTOR_PULL_ORIGINAL); strenv(FUNNEL_CI_EXECUTOR_PULL_NATIVE)) |
      to_yaml)) // .' -i "$output"
  plugin_count=$(yq eval-all -r '[select(.kind == "Deployment" and .metadata.name == "funnel-server") | .spec.template.spec.initContainers[]? | select(.name == "plugin")] | length' "$input")
  [[ "$plugin_count" == 1 ]] || { printf 'expected one Gen3 plugin init container\n' >&2; exit 1; }
yq eval '
  (select(.kind == "Deployment" and .metadata.name == "funnel-server") |
    (.spec.template.spec.initContainers[] | select(.name == "plugin")) |=
      (.image = strenv(FUNNEL_CI_PLUGIN_IMAGE_REF) | .imagePullPolicy = "Never")
  ) // .
' -i "$output"
fi
yq eval '
  (select(.kind == "Deployment" and .metadata.name == "funnel-server") |
    (.spec.template.spec.initContainers[] | select(.name == "config-updater")) |=
      (.image = strenv(FUNNEL_CI_AWSHELPER_IMAGE) | .imagePullPolicy = "IfNotPresent")
  ) // .
' -i "$output"

if [[ "$bootstrap_render" == true ]]; then
  yq eval '
    select(.kind == "Deployment") |= (.spec.replicas = 0) |
    select(.kind == "Job") |= (.spec.suspend = true | .spec.backoffLimit = 0) |
    select(.kind == "CronJob") |= (.spec.suspend = true | .spec.jobTemplate.spec.backoffLimit = 0)
  ' -i "$output"
  server_namespace=$(yq eval-all -r 'select(.kind == "Deployment" and .metadata.name == "funnel-server") | .metadata.namespace' "$output")
  server_account=$(yq eval-all -r 'select(.kind == "Deployment" and .metadata.name == "funnel-server") | .spec.template.spec.serviceAccountName' "$output")
  matching_accounts=$(SERVER_NAMESPACE="$server_namespace" SERVER_ACCOUNT="$server_account" yq eval-all -r '[select(.kind == "ServiceAccount" and .metadata.namespace == strenv(SERVER_NAMESPACE) and .metadata.name == strenv(SERVER_ACCOUNT))] | length' "$output")
  [[ "$matching_accounts" == 1 ]] || {
    printf 'Funnel service account %s is missing from server namespace %s\n' "$server_account" "$server_namespace" >&2
    exit 1
  }
  jobs_namespace=$(yq eval-all -r 'select(.kind == "ConfigMap" and .metadata.name == "funnel-server-config") | .data."funnel-server.yaml" | from_yaml | .Kubernetes.JobsNamespace' "$output")
  isolation_policies=$(JOBS_NAMESPACE="$jobs_namespace" yq eval-all -r '[select(.kind == "NetworkPolicy" and .metadata.name == "allow-nothing-netpolicy" and .metadata.namespace == strenv(JOBS_NAMESPACE))] | length' "$output")
  [[ "$isolation_policies" == 1 ]] || {
    printf 'executor network isolation is missing from namespace %s\n' "$jobs_namespace" >&2
    exit 1
  }
  cat >>"$output" <<EOF
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: funnel-executor-to-public-gateway
  namespace: ${jobs_namespace}
spec:
  podSelector:
    matchLabels:
      app: funnel-executor
  policyTypes: [Egress]
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: ${server_namespace}
          podSelector:
            matchLabels:
              app: revproxy
      ports:
        - protocol: TCP
          port: 80
EOF
  yq eval -N -o=json -I=0 '.' "$output" >"$render_dir/resources.jsonl"
  yq eval-all -o=json -I=0 'select(.kind == "Secret" and .metadata.name == "gen3workflow-g3auto") | .stringData."gen3-workflow-config.yaml" | from_yaml' "$output" >"$render_dir/gateway-config.json"
  python3 "$fixture_dir/render-dpop-gateway.py" "$render_dir/resources.jsonl" "$render_dir/gateway-config.json" >"$render_dir/dpop.yaml"
  mv "$render_dir/dpop.yaml" "$output"
  active_jobs=$(yq eval-all -r '[select(.kind == "Job") | select(.spec.suspend != true)] | length' "$output")
  live_deployments=$(yq eval-all -r '[select(.kind == "Deployment") | select(.spec.replicas != 0)] | length' "$output")
  if [[ "$active_jobs" != 0 || "$live_deployments" != 0 ]]; then
    printf 'bootstrap render left Jobs active or Deployments scaled up\n' >&2
    exit 1
  fi
fi

# Freeze public fixture services while leaving candidate and executor images intact.
if [[ -n "$FUNNEL_CI_PLUGIN_IMAGE_REF" ]]; then
yq eval '
  (.. | select(tag == "!!map" and has("image")) |
    select(.image == strenv(FUNNEL_CI_AWSHELPER_IMAGE))) |=
      (.image = strenv(FUNNEL_CI_NATIVE_AWSHELPER_IMAGE) | .imagePullPolicy = "Never")
' -i "$output"
yq eval '
  (.. | select(tag == "!!map" and has("image")) |
    select(.image == "quay.io/cdis/arborist:master")) |=
      (.image = "funnel-gen3-arborist:ci" | .imagePullPolicy = "Never")
' -i "$output"
fi

FIXTURE_IMAGE_LOCK="$fixture_dir/fixture-images.json" yq eval '
  load(strenv(FIXTURE_IMAGE_LOCK)).images as $images |
  (.. | select(tag == "!!map" and has("image")) | .image | select(tag == "!!str")) |=
    (. as $reference | $images[$reference].reference // $reference)
' "$output" >"$render_dir/pinned.yaml"
mv "$render_dir/pinned.yaml" "$output"

actual_image=$(yq eval-all -r 'select(.kind == "Deployment" and .metadata.name == "funnel-server") | .spec.template.spec.containers[] | select(.name == "funnel") | .image' "$output")
actual_plugin=$(yq eval-all -r 'select(.kind == "Deployment" and .metadata.name == "funnel-server") | .spec.template.spec.initContainers[] | select(.name == "plugin") | .image' "$output")
server_policy=$(yq eval-all -r 'select(.kind == "Deployment" and .metadata.name == "funnel-server") | .spec.template.spec.containers[] | select(.name == "funnel") | .imagePullPolicy' "$output")
worker_template=$(yq eval-all -r 'select(.kind == "ConfigMap" and .metadata.name == "funnel-server-config") | .data."funnel-server.yaml" | from_yaml | .Kubernetes.WorkerTemplate' "$output")
if [[ "$actual_image" != "$FUNNEL_CI_IMAGE_REF" || ( -n "$FUNNEL_CI_PLUGIN_IMAGE_REF" && "$actual_plugin" != "$FUNNEL_CI_PLUGIN_IMAGE_REF" ) || "$server_policy" != Never ]]; then
  printf 'post-rendered Funnel image mismatch: server=%s plugin=%s policy=%s\n' "$actual_image" "$actual_plugin" "$server_policy" >&2
  exit 1
fi
if [[ "$worker_template" != *'image: {{.Image}}'* || "$worker_template" != *'imagePullPolicy: Never'* || "$worker_template" == *'imagePullPolicy: Always'* ]]; then
  printf 'post-rendered Funnel worker image contract is invalid\n' >&2
  exit 1
fi
executor_template=$(yq eval-all -r 'select(.kind == "ConfigMap" and .metadata.name == "funnel-server-config") | .data."funnel-server.yaml" | from_yaml | .Kubernetes.ExecutorTemplate' "$output")
if [[ -n "$FUNNEL_CI_PLUGIN_IMAGE_REF" && "$executor_template" != *"$FUNNEL_CI_EXECUTOR_PULL_NATIVE"* ]]; then
  printf 'post-rendered executor template can overwrite native task fixtures\n' >&2
  exit 1
fi
printf 'rendered Funnel images (bootstrap=%s): %s, %s\n' "$bootstrap_render" "$FUNNEL_CI_IMAGE_REF" "$FUNNEL_CI_PLUGIN_IMAGE_REF" >&2
cat "$output"
