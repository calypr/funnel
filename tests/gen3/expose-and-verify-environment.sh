#!/usr/bin/env bash
set -Eeuo pipefail

: "${GITHUB_WORKSPACE:?}"
: "${GITHUB_ENV:?}"
: "${FUNNEL_CI_IMAGE_REF:?}"
: "${FUNNEL_CI_IMAGE_ID:?}"
: "${FUNNEL_CI_IMAGE_DIGEST:?}"
: "${FUNNEL_CI_IMAGE_CONFIG_ID:?}"
: "${FUNNEL_CI_PLUGIN_IMAGE_REF:?}"
: "${FUNNEL_CI_PLUGIN_IMAGE_ID:?}"
: "${FUNNEL_CI_PLUGIN_IMAGE_DIGEST:?}"
: "${FUNNEL_CI_PLUGIN_IMAGE_CONFIG_ID:?}"
: "${FUNNEL_CI_PLUGIN_SOURCE_SHA:?}"
: "${NAMESPACE:?}"

source_sha=$(git -C "$GITHUB_WORKSPACE" rev-parse HEAD)
if [[ "$source_sha" != "$FUNNEL_CI_SOURCE_SHA" ]]; then
  printf 'checked-out source changed from %s to %s during Gen3 setup\n' "$FUNNEL_CI_SOURCE_SHA" "$source_sha" >&2
  exit 1
fi
source_id="${FUNNEL_CI_SOURCE_ID:-$source_sha}"
if [[ "${FUNNEL_CI_REUSE_CLUSTER:-false}" == true ]]; then
  : "${FUNNEL_CI_STATE_DIR:?local image verification requires FUNNEL_CI_STATE_DIR}"
  current_source_id=$(python3 - "$GITHUB_WORKSPACE" "$FUNNEL_CI_STATE_DIR" <<'PY'
from pathlib import Path
import hashlib
import os
import shutil
import stat
import subprocess
import sys

workspace = Path(sys.argv[1]).resolve()
state_dir = Path(sys.argv[2]).resolve()
try:
    state_relative = state_dir.relative_to(workspace)
except ValueError:
    state_relative = None
paths = subprocess.check_output(
    ["git", "-C", str(workspace), "ls-files", "-co", "--exclude-standard", "-z"]
).split(b"\0")
digest = hashlib.sha256()
class _HashWriter:
    def __init__(self, target):
        self.target = target
    def write(self, data):
        self.target.update(data)
        return len(data)

for raw_path in sorted(item for item in paths if item):
    relative = os.fsdecode(raw_path)
    path = workspace / relative
    if state_relative is not None and (relative == str(state_relative) or relative.startswith(str(state_relative) + os.sep)):
        continue
    if not path.exists() and not path.is_symlink():
        continue
    info = path.lstat()
    digest.update(relative.encode("utf-8", "surrogateescape") + b"\0")
    digest.update(str(stat.S_IMODE(info.st_mode)).encode() + b"\0")
    if stat.S_ISLNK(info.st_mode):
        digest.update(b"link\0" + os.readlink(path).encode("utf-8", "surrogateescape"))
    elif stat.S_ISREG(info.st_mode):
        digest.update(b"file\0" + str(info.st_size).encode() + b"\0")
        with path.open("rb") as source:
            shutil.copyfileobj(source, _HashWriter(digest))
    elif stat.S_ISDIR(info.st_mode):
        digest.update(b"dir\0")
    else:
        digest.update(b"other\0")
    digest.update(b"\0")
print(digest.hexdigest())
PY
  )
  if [[ "$current_source_id" != "$source_id" ]]; then
    printf 'working-tree source changed during Gen3 setup: built %s, current %s\n' "$source_id" "$current_source_id" >&2
    exit 1
  fi
fi

kind_cluster="${FUNNEL_CI_KIND_CLUSTER_NAME:-kind-cluster}"
for node in "$kind_cluster-control-plane" "$kind_cluster-worker"; do
  if ! docker exec "$node" ctr --namespace k8s.io images ls | awk -v image="$FUNNEL_CI_IMAGE_REF" '$1 == image { found = 1 } END { exit !found }'; then
    printf 'candidate image is missing from Kind node %s\n' "$node" >&2
    exit 1
  fi
  digest=$(docker exec "$node" ctr --namespace k8s.io images ls | awk -v image="$FUNNEL_CI_IMAGE_REF" '$1 == image { digest = $3 } END { print digest }')
  printf 'loaded_node=%s image=%s digest=%s\n' "$node" "$FUNNEL_CI_IMAGE_REF" "$digest"
  if ! docker exec "$node" ctr --namespace k8s.io images ls | awk -v image="$FUNNEL_CI_PLUGIN_IMAGE_REF" '$1 == image { found = 1 } END { exit !found }'; then
    printf 'Gen3 plugin image is missing from Kind node %s\n' "$node" >&2
    exit 1
  fi
  plugin_digest=$(docker exec "$node" ctr --namespace k8s.io images ls | awk -v image="$FUNNEL_CI_PLUGIN_IMAGE_REF" '$1 == image { digest = $3 } END { print digest }')
  plugin_config_id=$(docker exec "$node" crictl inspecti "$FUNNEL_CI_PLUGIN_IMAGE_REF" | jq -er '.status.id')
  if [[ "$plugin_config_id" != "$FUNNEL_CI_PLUGIN_IMAGE_CONFIG_ID" || "$plugin_digest" != "$FUNNEL_CI_PLUGIN_IMAGE_DIGEST" ]]; then
    printf 'Gen3 plugin image identity differs on Kind node %s\n' "$node" >&2
    exit 1
  fi
  printf 'loaded_node=%s image=%s digest=%s config_id=%s\n' "$node" "$FUNNEL_CI_PLUGIN_IMAGE_REF" "$plugin_digest" "$plugin_config_id"
done

deployment=$(kubectl -n "$NAMESPACE" get deployment funnel-server -o json)
if ! jq -e --arg image "$FUNNEL_CI_IMAGE_REF" '
  any(.spec.template.spec.containers[]; .name == "funnel" and .image == $image and .imagePullPolicy == "Never")
' <<<"$deployment" >/dev/null; then
  printf 'Funnel server deployment does not select the loaded candidate image with pull disabled\n' >&2
  exit 1
fi
if ! jq -e --arg image "$FUNNEL_CI_PLUGIN_IMAGE_REF" '
  any(.spec.template.spec.initContainers[]; .name == "plugin" and .image == $image and .imagePullPolicy == "Never")
' <<<"$deployment" >/dev/null; then
  printf 'Funnel deployment does not select the loaded Gen3 plugin with pulls disabled\n' >&2
  exit 1
fi

pods=$(kubectl -n "$NAMESPACE" get pods -l app=funnel -o json)
if ! jq -e --arg image "$FUNNEL_CI_IMAGE_REF" --arg config_id "$FUNNEL_CI_IMAGE_CONFIG_ID" --arg digest "$FUNNEL_CI_IMAGE_DIGEST" '
  [.items[] | select(any(.spec.containers[]; .name == "funnel"))] as $pods |
  ($pods | length) > 0 and
  all($pods[];
    any(.spec.containers[]; .name == "funnel" and .image == $image and .imagePullPolicy == "Never") and
    any((.status.containerStatuses // [])[];
      .name == "funnel" and
      ((.imageID | endswith($config_id)) or (.imageID | endswith($digest)))
    )
  )
' <<<"$pods" >/dev/null; then
  printf 'Funnel server pod image IDs do not match the exact candidate image loaded into Kind\n' >&2
  exit 1
fi
if ! jq -e --arg image "$FUNNEL_CI_PLUGIN_IMAGE_REF" --arg config_id "$FUNNEL_CI_PLUGIN_IMAGE_CONFIG_ID" --arg digest "$FUNNEL_CI_PLUGIN_IMAGE_DIGEST" '
  [.items[] | select(any(.spec.initContainers[]?; .name == "plugin"))] as $pods |
  ($pods | length) > 0 and
  all($pods[];
    any(.spec.initContainers[]; .name == "plugin" and .image == $image and .imagePullPolicy == "Never") and
    any((.status.initContainerStatuses // [])[];
      .name == "plugin" and
      ((.imageID | endswith($config_id)) or (.imageID | endswith($digest)))
    )
  )
' <<<"$pods" >/dev/null; then
  printf 'Funnel plugin init-container image IDs do not match the exact pinned plugin image loaded into Kind\n' >&2
  exit 1
fi

worker_template=$(kubectl -n "$NAMESPACE" get configmap funnel-server-config -o json | \
  jq -r '.data["funnel-server.yaml"]' | \
  yq eval -r '.Kubernetes.WorkerTemplate' -)
if [[ "$worker_template" != *'image: {{.Image}}'* || "$worker_template" != *'imagePullPolicy: Never'* || "$worker_template" == *'imagePullPolicy: Always'* ]]; then
  printf 'Funnel worker template does not inherit the candidate server image without pulling\n' >&2
  exit 1
fi

artifacts_dir=${FUNNEL_CI_ARTIFACTS_DIR:-${ARTIFACTS_DIR:-$GITHUB_WORKSPACE/logs}}
if [[ "$artifacts_dir" != /* ]]; then
  artifacts_dir="$GITHUB_WORKSPACE/$artifacts_dir"
fi
mkdir -p "$artifacts_dir"
jq -n \
  --arg source_sha "$source_sha" \
  --arg image_ref "$FUNNEL_CI_IMAGE_REF" \
  --arg docker_image_id "$FUNNEL_CI_IMAGE_ID" \
  --arg image_digest "$FUNNEL_CI_IMAGE_DIGEST" \
  --arg image_config_id "$FUNNEL_CI_IMAGE_CONFIG_ID" \
  --arg plugin_source_sha "$FUNNEL_CI_PLUGIN_SOURCE_SHA" \
  --arg plugin_image_ref "$FUNNEL_CI_PLUGIN_IMAGE_REF" \
  --arg plugin_image_id "$FUNNEL_CI_PLUGIN_IMAGE_ID" \
  --arg plugin_image_digest "$FUNNEL_CI_PLUGIN_IMAGE_DIGEST" \
  --arg plugin_image_config_id "$FUNNEL_CI_PLUGIN_IMAGE_CONFIG_ID" \
  --argjson deployment "$deployment" \
  --argjson pods "$pods" \
  '{source_sha: $source_sha, image_ref: $image_ref, docker_image_id: $docker_image_id, image_digest: $image_digest, image_config_id: $image_config_id, plugin: {source_sha: $plugin_source_sha, image_ref: $plugin_image_ref, docker_image_id: $plugin_image_id, image_digest: $plugin_image_digest, image_config_id: $plugin_image_config_id}, server: {deployment_image: [$deployment.spec.template.spec.containers[] | select(.name == "funnel") | .image], plugin_image: [$deployment.spec.template.spec.initContainers[] | select(.name == "plugin") | .image], pods: [$pods.items[] | select(any(.spec.containers[]; .name == "funnel")) | {name: .metadata.name, node: .spec.nodeName, containers: [.status.containerStatuses[]? | select(.name == "funnel") | {image, imageID}], initContainers: [.status.initContainerStatuses[]? | select(.name == "plugin") | {image, imageID}]}]}}' \
  >"$artifacts_dir/gen3-image-identity.json"
cat "$artifacts_dir/gen3-image-identity.json"

setup_dir=$(mktemp -d)
trap 'rm -rf "$setup_dir"' EXIT
bash "$GITHUB_WORKSPACE/tests/gen3/start-port-forward.sh" "$NAMESPACE" "$artifacts_dir/port-forward.log"

# Detect a disabled or misrouted DPoP gateway before starting executor tests.
dpop_status=$(curl --silent --show-error --max-time 5 \
  -H 'Authorization: DPoP invalid' -H 'DPoP: invalid' \
  --output "$setup_dir/dpop-rejection.json" --write-out '%{http_code}' \
  "${HOSTNAME_PROTOCOL:-http}://${HOSTNAME:-localhost:8000}/ga4gh/tes/v1/tasks")
if [[ "$dpop_status" != 401 ]] || ! jq -e '.error == "invalid_dpop_proof"' "$setup_dir/dpop-rejection.json" >/dev/null; then
  printf 'DPoP gateway did not reject an invalid proof correctly (HTTP %s)\n' "$dpop_status" >&2
  exit 1
fi
printf 'DPoP gateway validates proofs; invalid proof rejected with HTTP 401\n'
