#!/usr/bin/env bash
set -Eeuo pipefail
script_dir=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cat >"$tmp/input.yaml" <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: funnel-server
spec:
  template:
    spec:
      initContainers:
        - name: config-updater
          image: quay.io/cdis/awshelper:master
        - name: plugin
          image: example/plugin:old
      containers:
        - name: funnel
          image: example/funnel:old
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: funnel-server-config
data:
  funnel-server.yaml: |
    Kubernetes:
      ExecutorTemplate: |
        containers:
          - image: {{.Image}}
            imagePullPolicy: {{if .ImagePullPolicy}}{{.ImagePullPolicy}}{{else}}Always{{end}}
      WorkerTemplate: |
        containers:
          - image: {{.Image}}
            imagePullPolicy: Always
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: arborist-deployment
spec:
  template:
    spec:
      containers:
        - name: arborist
          image: quay.io/cdis/arborist:master
YAML
export FUNNEL_CI_IMAGE_REF=quay.io/ohsu-comp-bio/funnel:ci-123456789abc
registry_helper=$(yq -r '.global.awshelper_container_image' "$script_dir/values-ci.yaml")
for mode in plain gen3; do
  export FUNNEL_CI_PLUGIN_IMAGE_REF=
  if [[ "$mode" == gen3 ]]; then
    export FUNNEL_CI_PLUGIN_IMAGE_REF=quay.io/ohsu-comp-bio/funnel-gen3-plugin:ci-123456789abc-eacaf197bfbc
  fi
  FUNNEL_CI_BOOTSTRAP_RENDER=false bash "$script_dir/gen3-post-renderer.sh" <"$tmp/input.yaml" >"$tmp/$mode.yaml"
  executor=$(yq -r 'select(.kind == "ConfigMap") | .data."funnel-server.yaml" | from_yaml | .Kubernetes.ExecutorTemplate' "$tmp/$mode.yaml")
  if [[ "$mode" == gen3 ]]; then
    [[ "$executor" == *'eq .Image "quay.io/cdis/gen3-workflow:integration_tests_dicom_image"'* && "$executor" == *'}}Never{{else}}'* ]] || { printf 'Native executors can be overwritten by registry pulls\n' >&2; exit 1; }
  else
    [[ "$executor" == *'imagePullPolicy: {{if .ImagePullPolicy}}{{.ImagePullPolicy}}{{else}}Always{{end}}'* ]]
  fi
  helper=$(yq -r 'select(.kind == "Deployment") | .spec.template.spec.initContainers[] | select(.name == "config-updater") | .image' "$tmp/$mode.yaml")
  if [[ "$mode" == plain ]]; then
    [[ "$helper" == "$registry_helper" ]] || { printf 'Plain chart selected a Gen3-only local helper\n' >&2; exit 1; }
  else
    [[ "$helper" == funnel-gen3-awshelper:ci ]] || { printf 'Gen3 chart did not select its native helper\n' >&2; exit 1; }
    policy=$(yq -r 'select(.kind == "Deployment") | .spec.template.spec.initContainers[] | select(.name == "config-updater") | .imagePullPolicy' "$tmp/$mode.yaml")
    [[ "$policy" == Never ]]
  fi
  arborist=$(yq -r 'select(.metadata.name == "arborist-deployment") | .spec.template.spec.containers[0].image' "$tmp/$mode.yaml")
  if [[ "$mode" == gen3 ]]; then
    [[ "$arborist" == funnel-gen3-arborist:ci ]]
  else
    [[ "$arborist" == quay.io/cdis/arborist:*@sha256:* ]]
  fi
  printf 'PASS: %s chart selects the correct helper and Arborist images\n' "$mode"
done

sed 's/imagePullPolicy: {{if .ImagePullPolicy}}{{.ImagePullPolicy}}{{else}}Always{{end}}/imagePullPolicy: Always/' "$tmp/input.yaml" >"$tmp/changed-executor.yaml"
if FUNNEL_CI_BOOTSTRAP_RENDER=false bash "$script_dir/gen3-post-renderer.sh" <"$tmp/changed-executor.yaml" >"$tmp/rejected.yaml" 2>"$tmp/rejected.log"; then
  printf 'Changed executor template bypassed the native fixture pull policy\n' >&2
  exit 1
fi
grep -q 'executor template can overwrite native task fixtures' "$tmp/rejected.log"
printf 'PASS: changed executor templates fail before cluster startup\n'
