#!/usr/bin/env bash
set -Eeuo pipefail

: "${GITHUB_WORKSPACE:?}"
: "${GITHUB_ENV:?}"
plugin_source_sha=eacaf197bfbc786c753d9ecfb86a2a2f7022a5e3
if [[ ! "$plugin_source_sha" =~ ^[0-9a-f]{40}$ ]]; then
  printf 'invalid Gen3 authorizer plugin commit: %s\n' "$plugin_source_sha" >&2
  exit 1
fi

workspace=$(cd "$GITHUB_WORKSPACE" && pwd)
PYTHONDONTWRITEBYTECODE=1 python3 "$workspace/tests/test-image-config-digest.py"
source_sha=$(git -C "$workspace" rev-parse HEAD)
reuse_cluster="${FUNNEL_CI_REUSE_CLUSTER:-false}"
build_only="${FUNNEL_CI_BUILD_ONLY:-false}"
if [[ "$build_only" != true && "$build_only" != false ]]; then
  printf 'FUNNEL_CI_BUILD_ONLY must be true or false\n' >&2
  exit 1
fi
if [[ "$build_only" == true && "$reuse_cluster" != true ]]; then
  printf 'Image-only builds require the explicit local state configuration\n' >&2
  exit 1
fi
if [[ "$reuse_cluster" != true && "$reuse_cluster" != false ]]; then
  printf 'FUNNEL_CI_REUSE_CLUSTER must be true or false, got: %s\n' "$reuse_cluster" >&2
  exit 1
fi
if [[ "$reuse_cluster" == true ]]; then
  : "${FUNNEL_CI_STATE_DIR:?local cluster reuse requires an explicit FUNNEL_CI_STATE_DIR}"
  : "${FUNNEL_CI_KUBECONFIG:?local cluster reuse requires an explicit FUNNEL_CI_KUBECONFIG}"
  : "${FUNNEL_CI_KIND_CLUSTER_NAME:?local cluster reuse requires an explicit FUNNEL_CI_KIND_CLUSTER_NAME}"
  [[ "$FUNNEL_CI_STATE_DIR" == /* && "$FUNNEL_CI_KUBECONFIG" == /* ]] || {
    printf 'local state directory and kubeconfig paths must be absolute\n' >&2
    exit 1
  }
  source_id=$(python3 - "$workspace" "$FUNNEL_CI_STATE_DIR" <<'PY'
from pathlib import Path
import hashlib
import os
import shutil
import stat
import subprocess
import sys

workspace = Path(sys.argv[1]).resolve()
state_dir = Path(sys.argv[2]).resolve()
state_relative = None
try:
    state_relative = state_dir.relative_to(workspace)
except ValueError:
    pass
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
  if [[ ! "$source_id" =~ ^[0-9a-f]{64}$ ]]; then
    printf 'could not calculate a valid local source identity: %s\n' "$source_id" >&2
    exit 1
  fi
  candidate_image="quay.io/ohsu-comp-bio/funnel:local-${source_id}"
  plugin_image="quay.io/ohsu-comp-bio/funnel-gen3-plugin:local-${source_id}-${plugin_source_sha:0:12}"
else
  source_id="$source_sha"
  candidate_image="quay.io/ohsu-comp-bio/funnel:ci-${source_sha:0:12}"
  plugin_image="quay.io/ohsu-comp-bio/funnel-gen3-plugin:ci-${source_sha:0:12}-${plugin_source_sha:0:12}"
fi
build_platform="${FUNNEL_CI_BUILD_PLATFORM:-linux/amd64}"
if [[ ! "$build_platform" =~ ^linux/(amd64|arm64)$ ]]; then
  printf 'unsupported Gen3 build platform: %s\n' "$build_platform" >&2
  exit 1
fi
target_arch="${build_platform#linux/}"
kind_cluster="${FUNNEL_CI_KIND_CLUSTER_NAME:-kind-cluster}"
kind_node_image='kindest/node:v1.35.0@sha256:4613778f3cfcd10e615029370f5786704559103cf27bef934597ba562b269661'
task_id="${GITHUB_RUN_ID:-local-${source_sha:0:12}}-${GITHUB_RUN_ATTEMPT:-1}"
state_dir="${FUNNEL_CI_STATE_DIR:-${RUNNER_TEMP:-/tmp}/funnel-gen3-${task_id}}"
kubeconfig="${FUNNEL_CI_KUBECONFIG:-$state_dir/kubeconfig}"
ownership_marker="$state_dir/kind-cluster-ownership"
go_cache_dir="${FUNNEL_CI_GO_CACHE_DIR:-${RUNNER_TEMP:-/tmp}/funnel-gen3-go-cache}"
go_build_cache="$go_cache_dir/build"
go_module_cache="$go_cache_dir/module"
mkdir -p "$state_dir" "$go_build_cache" "$go_module_cache"
setup_dir=$(mktemp -d)
kind_config="$setup_dir/kind.yaml"
kind_log="$setup_dir/kind-create.log"
builder_log="$setup_dir/go-builder-build.log"
if [[ -n "${FUNNEL_CI_ARTIFACTS_DIR:-}" ]]; then
  mkdir -p "$FUNNEL_CI_ARTIFACTS_DIR"
  builder_log="$FUNNEL_CI_ARTIFACTS_DIR/image-build.log"
  kind_log="$FUNNEL_CI_ARTIFACTS_DIR/kind-create.log"
fi
build_token="$source_sha-$$-$RANDOM"
image_archive="$setup_dir/candidate-image.tar"
plugin_archive="$setup_dir/gen3-plugin.tar.gz"
plugin_context="$setup_dir/gen3-plugin"
plugin_image_archive="$setup_dir/gen3-plugin-image.tar"
plugin_output="$setup_dir/gen3-plugin-output"
build_context="$setup_dir/build-context"
builder_dockerfile="$workspace/tests/gen3/image-build/Dockerfile"
runtime_dockerfile="$setup_dir/Dockerfile.funnel-runtime"
builder_image="funnel-gen3-go-builder:ci-${source_sha:0:12}-${target_arch}"
build_pid=""
kind_pid=""

cleanup() {
  status=$?
  trap - EXIT
  # Stop daemon-owned compiler containers before removing their bind mounts.
  local_container_ids=$(timeout 5 docker ps -aq --filter "label=io.calypr.funnel.gen3-build=$build_token" 2>/dev/null || true)
  for container_id in $local_container_ids; do
    timeout 10 docker rm -f "$container_id" >/dev/null 2>&1 || true
  done
  for pid in "$build_pid" "$kind_pid"; do
    if [[ -n "$pid" ]]; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  if (( status != 0 )); then
    if [[ -f "$builder_log" ]]; then
      tail -n 30 "$builder_log"
    fi
    if [[ -f "$kind_log" ]]; then
      tail -n 30 "$kind_log"
    fi
  fi
  rm -rf "$setup_dir" || printf 'Retained build scratch directory: %s\n' "$setup_dir" >&2
  exit "$status"
}
trap cleanup EXIT

printf 'source_commit=%s\nsource_id=%s\ncandidate_image=%s\nbuild_platform=%s\n' "$source_sha" "$source_id" "$candidate_image" "$build_platform"
printf 'gen3_plugin_commit=%s\ngen3_plugin_image=%s\n' "$plugin_source_sha" "$plugin_image"
printf 'go_cache_dir=%s\nkubeconfig=%s\nkind_node_image=%s\n' "$go_cache_dir" "$kubeconfig" "$kind_node_image"
export KUBECONFIG="$kubeconfig"
printf 'FUNNEL_CI_KUBECONFIG=%s\n' "$kubeconfig" >>"$GITHUB_ENV"

cluster_exists=false
if kind get clusters | awk -v name="$kind_cluster" '$0 == name { found = 1 } END { exit !found }'; then
  cluster_exists=true
fi
if [[ "$reuse_cluster" == true ]]; then
  if [[ -f "$ownership_marker" ]]; then
    expected_marker=$(printf 'workspace=%s\ncluster=%s\nkubeconfig=%s\n' "$workspace" "$kind_cluster" "$(cd "$(dirname "$kubeconfig")" && pwd)/$(basename "$kubeconfig")")
    if [[ "$(cat "$ownership_marker")" != "$expected_marker" ]]; then
      printf 'Kind ownership marker in %s does not match workspace, cluster, and explicit kubeconfig\n' "$state_dir" >&2
      exit 1
    fi
    if [[ "$cluster_exists" != true ]]; then
      printf 'owned Kind cluster %s is missing\n' "$kind_cluster" >&2
      exit 1
    fi
    if [[ ! -f "$kubeconfig" ]]; then
      printf 'owned kubeconfig is missing: %s\n' "$kubeconfig" >&2
      exit 1
    fi
    configured_context=$(KUBECONFIG="$kubeconfig" kubectl config current-context 2>/dev/null || true)
    configured_cluster=$(KUBECONFIG="$kubeconfig" kubectl config view --minify -o jsonpath='{.contexts[0].context.cluster}' 2>/dev/null || true)
    if [[ "$configured_context" != "kind-$kind_cluster" || "$configured_cluster" != "kind-$kind_cluster" ]]; then
      printf 'explicit kubeconfig %s does not target Kind cluster %s\n' "$kubeconfig" "$kind_cluster" >&2
      exit 1
    fi
    cluster_reused=true
  elif [[ "$cluster_exists" == true ]]; then
    printf 'Kind cluster %s exists without the ownership marker in %s; refusing to reuse it\n' "$kind_cluster" "$state_dir" >&2
    exit 1
  else
    cluster_reused=false
  fi
else
  if [[ "$cluster_exists" == true ]]; then
    printf 'Kind cluster %s already exists; refusing to reuse or change it\n' "$kind_cluster" >&2
    exit 1
  fi
  cluster_reused=false
fi

cat >"$kind_config" <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".containerd]
    discard_unpacked_layers = false
nodes:
- role: control-plane
  image: $kind_node_image
  kubeadmConfigPatches:
  - |
    kind: KubeletConfiguration
    serializeImagePulls: false
    maxParallelImagePulls: 4
- role: worker
  image: $kind_node_image
  labels:
    role: workflow
EOF

mkdir -p "$build_context"
if [[ "$reuse_cluster" == true ]]; then
  python3 - "$workspace" "$FUNNEL_CI_STATE_DIR" "$build_context" <<'PY'
from pathlib import Path
import os
import shutil
import subprocess
import sys

workspace, state_dir, destination = map(Path, sys.argv[1:])
workspace = workspace.resolve()
state_dir = state_dir.resolve()
destination = destination.resolve()
try:
    state_relative = state_dir.relative_to(workspace)
except ValueError:
    state_relative = None
paths = subprocess.check_output(
    ["git", "-C", str(workspace), "ls-files", "-co", "--exclude-standard", "-z"]
).split(b"\0")
for raw_path in sorted(item for item in paths if item):
    relative = os.fsdecode(raw_path)
    source = workspace / relative
    if state_relative is not None and (relative == str(state_relative) or relative.startswith(str(state_relative) + os.sep)):
        continue
    if not source.exists() and not source.is_symlink():
        continue
    target = destination / relative
    target.parent.mkdir(parents=True, exist_ok=True)
    if source.is_symlink():
        target.symlink_to(os.readlink(source))
    elif source.is_dir():
        target.mkdir(exist_ok=True)
        shutil.copystat(source, target)
    else:
        shutil.copy2(source, target)
PY
else
  git -C "$workspace" archive --format=tar HEAD | tar -xf - -C "$build_context"
fi

go_builder_image=$(awk '$1 == "FROM" && $3 == "AS" && $4 == "build-env" { print $2; exit }' "$build_context/Dockerfile")
if [[ -z "$go_builder_image" ]]; then
  printf 'could not find the production build-env Go image in Dockerfile\n' >&2
  exit 1
fi
nerdctl_version=$(awk '$1 == "ARG" && $2 ~ /^NERDCTL_VERSION=/ { sub(/^NERDCTL_VERSION=/, "", $2); print $2; exit }' "$build_context/Dockerfile")
if [[ -z "$nerdctl_version" ]]; then
  printf 'could not find the production nerdctl version in Dockerfile\n' >&2
  exit 1
fi

python3 - "$build_context/Dockerfile" "$runtime_dockerfile" <<'PY'
from pathlib import Path
import sys

source = Path(sys.argv[1]).read_text()
marker = "# final stage\n"
if source.count(marker) != 1:
    raise SystemExit("The production Dockerfile final-stage marker changed")
runtime = source.split(marker, 1)[1]
lines = runtime.splitlines()
binary_copies = [i for i, line in enumerate(lines) if line.startswith("COPY --from=build-env ") and line.endswith("/funnel /app/")]
nerdctl_copies = [i for i, line in enumerate(lines) if line == "COPY --from=build-env /tmp/nerdctl /usr/local/bin/nerdctl"]
if len(binary_copies) != 1 or len(nerdctl_copies) != 1:
    raise SystemExit("The production runtime image COPY instructions changed")
lines[binary_copies[0]] = "COPY funnel /app/"
lines[nerdctl_copies[0]] = "COPY nerdctl /usr/local/bin/nerdctl"
Path(sys.argv[2]).write_text("\n".join(lines) + "\n")
PY

build_images() {
  local builder_started builder_ready candidate_compile_started candidate_compile_finished
  local candidate_package_started candidate_package_finished plugin_build_started plugin_build_finished
  local funnel_module plugin_funnel_module

  # Local repeats may reuse only images for these exact working-tree bytes.
  # Hosted CI always builds its own candidate.
  local cached_images
  if [[ "$reuse_cluster" == true ]] && \
    cached_images=$(docker image inspect "$candidate_image" "$plugin_image" 2>/dev/null) && \
    printf '%s' "$cached_images" | jq -e \
      --arg arch "$target_arch" --arg source "$source_id" \
      --arg commit "$source_sha" --arg plugin "$plugin_source_sha" '
        length == 2 and
        all(.[]; .Os == "linux" and .Architecture == $arch and
          .Config.Labels["io.ohsu-comp-bio.funnel.source-id"] == $source) and
        .[0].Config.Labels["org.opencontainers.image.revision"] == $commit and
        .[1].Config.Labels["org.opencontainers.image.revision"] == $plugin
      ' >/dev/null; then
    docker save "$candidate_image" >"$image_archive"
    docker save "$plugin_image" >"$plugin_image_archive"
    printf 'candidate_images_reused=true\n' >"$setup_dir/build-timings"
    printf 'Reusing exact local source images: %s %s\n' "$candidate_image" "$plugin_image"
    return
  fi

  builder_started=$(date +%s)
  DOCKER_BUILDKIT=1 docker build --platform "$build_platform" \
    --file "$builder_dockerfile" --target go-builder \
    --build-arg "GO_BUILDER_IMAGE=$go_builder_image" \
    --build-arg "WORKSPACE_DIR=/workspace" \
    --build-arg "NERDCTL_VERSION=$nerdctl_version" \
    --build-arg "TARGETARCH=$target_arch" \
    --tag "$builder_image" "$build_context"
  builder_ready=$(date +%s)

  candidate_compile_started=$(date +%s)
  docker run --rm --platform "$build_platform" \
    --label "io.calypr.funnel.gen3-build=$build_token" \
    --volume "$build_context:/workspace" \
    --volume "$go_build_cache:/root/.cache/go-build" \
    --volume "$go_module_cache:/go/pkg/mod" \
    --env GOCACHE=/root/.cache/go-build \
    --env GOPATH=/go \
    --workdir /workspace \
    "$builder_image" \
    bash -euo pipefail -c 'sh .github/scripts/download-go-modules.sh && make build && cp /tmp/nerdctl nerdctl'
  candidate_compile_finished=$(date +%s)

  candidate_package_started=$(date +%s)
  DOCKER_BUILDKIT=1 docker build --platform "$build_platform" \
    --file "$runtime_dockerfile" \
    --label "org.opencontainers.image.revision=$source_sha" \
    --label "io.ohsu-comp-bio.funnel.source-id=$source_id" \
    --tag "$candidate_image" "$build_context"
  candidate_package_finished=$(date +%s)

  mkdir -p "$plugin_context" "$plugin_output"
  curl --fail --silent --show-error --location --retry 3 --max-time 60 \
    "https://github.com/calypr/funnel-gen3-plugin/archive/${plugin_source_sha}.tar.gz" \
    --output "$plugin_archive"
  tar -xzf "$plugin_archive" --strip-components=1 -C "$plugin_context"

  funnel_module=$(awk '$1 == "module" { module = $2 } END { print module }' "$build_context/go.mod")
  if [[ -z "$funnel_module" ]]; then
    printf 'could not determine current Funnel module path\n' >&2
    return 1
  fi
  plugin_funnel_module=$(awk '$1 == "github.com/ohsu-comp-bio/funnel" || $1 == "github.com/calypr/funnel" { print $1; exit }' "$plugin_context/go.mod")
  if [[ -z "$plugin_funnel_module" ]]; then
    printf 'Gen3 plugin go.mod does not declare a supported Funnel module dependency\n' >&2
    return 1
  fi
  python3 - "$plugin_context" "$plugin_funnel_module" "$funnel_module" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
old, new = sys.argv[2:]
for path in [root / "go.mod", *root.rglob("*.go")]:
    source = path.read_text()
    if old in source:
        path.write_text(source.replace(old, new))
PY

  plugin_build_started=$(date +%s)
  docker run --rm --platform "$build_platform" \
    --label "io.calypr.funnel.gen3-build=$build_token" \
    --volume "$build_context:/workspace/funnel:ro" \
    --volume "$plugin_context:/workspace/plugin" \
    --volume "$plugin_output:/out" \
    --volume "$go_build_cache:/root/.cache/go-build" \
    --volume "$go_module_cache:/go/pkg/mod" \
    --env GOCACHE=/root/.cache/go-build \
    --env GOPATH=/go \
    --env "FUNNEL_MODULE=$funnel_module" \
    --env FUNNEL_MODULE_DIR=/workspace/funnel \
    --workdir /workspace/plugin \
    "$builder_image" \
    bash -euo pipefail -c '
      go mod edit "-replace=${FUNNEL_MODULE}=${FUNNEL_MODULE_DIR}"
      for attempt in 1 2 3; do
        if go mod tidy; then break; fi
        test "$attempt" -lt 3 || exit 1
        sleep "$attempt"
      done
      CGO_ENABLED=0 go build -trimpath -o /out/authorizer ./plugin-gen3
    '
  install -m 0755 "$plugin_output/authorizer" "$build_context/authorizer-binary"
  DOCKER_BUILDKIT=1 docker build --platform "$build_platform" \
    --file "$builder_dockerfile" --target gen3-plugin \
    --build-arg "GO_BUILDER_IMAGE=$go_builder_image" \
    --label "org.opencontainers.image.revision=$plugin_source_sha" \
    --label "io.ohsu-comp-bio.funnel.source-id=$source_id" \
    --label "org.opencontainers.image.source=https://github.com/calypr/funnel-gen3-plugin" \
    --tag "$plugin_image" "$build_context"
  plugin_build_finished=$(date +%s)

  docker save "$candidate_image" >"$image_archive"
  docker save "$plugin_image" >"$plugin_image_archive"
  {
    printf 'go_builder_setup_seconds=%s\n' "$((builder_ready - builder_started))"
    printf 'candidate_compile_seconds=%s\n' "$((candidate_compile_finished - candidate_compile_started))"
    printf 'candidate_package_seconds=%s\n' "$((candidate_package_finished - candidate_package_started))"
    printf 'candidate_build_seconds=%s\n' "$((candidate_package_finished - candidate_compile_started))"
    printf 'gen3_plugin_build_seconds=%s\n' "$((plugin_build_finished - plugin_build_started))"
  } >"$setup_dir/build-timings"
}

build_images >"$builder_log" 2>&1 &
build_pid=$!
if [[ "$build_only" == true ]]; then
  wait "$build_pid"
  build_pid=""
  cat "$setup_dir/build-timings"
  printf 'Built local images without creating or loading a Kind cluster: %s %s\n' "$candidate_image" "$plugin_image"
  exit 0
fi
kind_started=$(date +%s)
{
  if [[ "$reuse_cluster" == true ]]; then
    printf 'FUNNEL_CI_CLUSTER_OWNED=false\n'
  else
    printf 'FUNNEL_CI_CLUSTER_OWNED=true\n'
  fi
  printf 'FUNNEL_CI_KIND_CLUSTER_NAME=%s\n' "$kind_cluster"
  printf 'FUNNEL_CI_SOURCE_ID=%s\n' "$source_id"
  printf 'FUNNEL_CI_REUSE_CLUSTER=%s\n' "$reuse_cluster"
} >>"$GITHUB_ENV"
if [[ "$cluster_reused" != true ]]; then
  kind create cluster --name "$kind_cluster" --config "$kind_config" --kubeconfig "$kubeconfig" --wait 180s >"$kind_log" 2>&1 &
  kind_pid=$!
  if ! wait "$kind_pid"; then
    kind_pid=""
    exit 1
  fi
  kind_pid=""
  if [[ "$reuse_cluster" == true ]]; then
    marker_tmp="$ownership_marker.tmp.$$"
    printf 'workspace=%s\ncluster=%s\nkubeconfig=%s\n' "$workspace" "$kind_cluster" "$(cd "$(dirname "$kubeconfig")" && pwd)/$(basename "$kubeconfig")" >"$marker_tmp"
    mv "$marker_tmp" "$ownership_marker"
  fi
fi
printf 'FUNNEL_CI_CLUSTER_OWNED=true\n' >>"$GITHUB_ENV"
auth_images_ready_file=${FUNNEL_CI_AUTH_IMAGES_READY_FILE:-$state_dir/auth-images-ready}
rm -f "$auth_images_ready_file"
printf 'FUNNEL_CI_AUTH_IMAGES_READY_FILE=%s\n' "$auth_images_ready_file" >>"$GITHUB_ENV"
python3 "$workspace/tests/gen3/cache-fixture-images.py" restore-bootstrap
cluster_ready=$(date +%s)
kind_ready_file=${FUNNEL_CI_KIND_READY_FILE:-$state_dir/kind-ready}
printf 'cluster=%s\nkubeconfig=%s\n' "$kind_cluster" "$kubeconfig" >"$kind_ready_file.tmp.$$"
mv "$kind_ready_file.tmp.$$" "$kind_ready_file"
python3 "$workspace/tests/gen3/cache-fixture-images.py" restore-auth
touch "$auth_images_ready_file"
python3 "$workspace/tests/gen3/cache-fixture-images.py" restore-services


if ! wait "$build_pid"; then
  build_pid=""
  exit 1
fi
build_pid=""

kind get nodes --name "$kind_cluster"
candidate_image_id=$(docker image inspect "$candidate_image" --format '{{.Id}}')
candidate_revision=$(docker image inspect "$candidate_image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')
candidate_source_id=$(docker image inspect "$candidate_image" --format '{{index .Config.Labels "io.ohsu-comp-bio.funnel.source-id"}}')
if [[ "$candidate_revision" != "$source_sha" ]]; then
  printf 'candidate image revision %s does not match checked-out commit %s\n' "$candidate_revision" "$source_sha" >&2
  exit 1
fi
if [[ "$candidate_source_id" != "$source_id" ]]; then
  printf 'candidate image source identity %s does not match build input %s\n' "$candidate_source_id" "$source_id" >&2
  exit 1
fi
plugin_image_id=$(docker image inspect "$plugin_image" --format '{{.Id}}')
plugin_revision=$(docker image inspect "$plugin_image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')
if [[ "$plugin_revision" != "$plugin_source_sha" ]]; then
  printf 'plugin image revision %s does not match pinned source %s\n' "$plugin_revision" "$plugin_source_sha" >&2
  exit 1
fi
cat "$setup_dir/build-timings"

candidate_config_id=$(python3 "$workspace/tests/image-config-digest.py" "$image_archive" "$candidate_image")
plugin_config_id_expected=$(python3 "$workspace/tests/image-config-digest.py" "$plugin_image_archive" "$plugin_image")
printf 'candidate Docker ID=%s archived config=%s\n' "$candidate_image_id" "$candidate_config_id"
printf 'plugin Docker ID=%s archived config=%s\n' "$plugin_image_id" "$plugin_config_id_expected"

import_started=$(date +%s)
node_digests=()
node_config_ids=()
plugin_node_digests=()
plugin_node_config_ids=()
for node in "$kind_cluster-control-plane" "$kind_cluster-worker"; do
  docker exec -i "$node" ctr --namespace k8s.io images import --snapshotter overlayfs - <"$image_archive"
  docker exec -i "$node" ctr --namespace k8s.io images import --snapshotter overlayfs - <"$plugin_image_archive"
  if ! docker exec "$node" ctr --namespace k8s.io images ls | awk -v image="$candidate_image" '$1 == image { found = 1 } END { exit !found }'; then
    printf 'candidate image was not imported into Kind node %s\n' "$node" >&2
    exit 1
  fi
  digest=$(docker exec "$node" ctr --namespace k8s.io images ls | awk -v image="$candidate_image" '$1 == image { digest = $3 } END { print digest }')
  config_id=$(docker exec "$node" crictl inspecti "$candidate_image" | jq -er '.status.id')
  if ! docker exec "$node" ctr --namespace k8s.io images ls | awk -v image="$plugin_image" '$1 == image { found = 1 } END { exit !found }'; then
    printf 'Gen3 plugin image was not imported into Kind node %s\n' "$node" >&2
    exit 1
  fi
  plugin_digest=$(docker exec "$node" ctr --namespace k8s.io images ls | awk -v image="$plugin_image" '$1 == image { digest = $3 } END { print digest }')
  plugin_config_id=$(docker exec "$node" crictl inspecti "$plugin_image" | jq -er '.status.id')
  node_digests+=("$digest")
  node_config_ids+=("$config_id")
  plugin_node_digests+=("$plugin_digest")
  plugin_node_config_ids+=("$plugin_config_id")
  printf 'node=%s image=%s digest=%s config_id=%s\n' "$node" "$candidate_image" "$digest" "$config_id"
  printf 'node=%s image=%s digest=%s config_id=%s\n' "$node" "$plugin_image" "$plugin_digest" "$plugin_config_id"
done
if [[ "${node_digests[0]}" != "${node_digests[1]}" || "${node_config_ids[0]}" != "$candidate_config_id" || "${node_config_ids[1]}" != "$candidate_config_id" ]]; then
  printf 'Kind nodes did not import the exact candidate image identity\n' >&2
  exit 1
fi
if [[ "${plugin_node_digests[0]}" != "${plugin_node_digests[1]}" || "${plugin_node_config_ids[0]}" != "$plugin_config_id_expected" || "${plugin_node_config_ids[1]}" != "$plugin_config_id_expected" ]]; then
  printf 'Kind nodes did not import the exact Gen3 plugin image identity\n' >&2
  exit 1
fi
import_finished=$(date +%s)

printf 'candidate_image_id=%s\n' "$candidate_image_id"
printf 'gen3_plugin_image_id=%s\n' "$plugin_image_id"
printf 'kind_setup_seconds=%s\n' "$((cluster_ready - kind_started))"
cat "$setup_dir/build-timings"
printf 'candidate_import_seconds=%s\n' "$((import_finished - import_started))"
{
  printf 'FUNNEL_CI_IMAGE_REF=%s\n' "$candidate_image"
  printf 'FUNNEL_CI_IMAGE_ID=%s\n' "$candidate_image_id"
  printf 'FUNNEL_CI_IMAGE_DIGEST=%s\n' "${node_digests[0]}"
  printf 'FUNNEL_CI_IMAGE_CONFIG_ID=%s\n' "${node_config_ids[0]}"
  printf 'FUNNEL_CI_SOURCE_SHA=%s\n' "$source_sha"
  printf 'FUNNEL_CI_SOURCE_ID=%s\n' "$source_id"
  printf 'FUNNEL_CI_PLUGIN_IMAGE_REF=%s\n' "$plugin_image"
  printf 'FUNNEL_CI_PLUGIN_IMAGE_ID=%s\n' "$plugin_image_id"
  printf 'FUNNEL_CI_PLUGIN_IMAGE_DIGEST=%s\n' "${plugin_node_digests[0]}"
  printf 'FUNNEL_CI_PLUGIN_IMAGE_CONFIG_ID=%s\n' "${plugin_node_config_ids[0]}"
  printf 'FUNNEL_CI_PLUGIN_SOURCE_SHA=%s\n' "$plugin_source_sha"
} >>"$GITHUB_ENV"
