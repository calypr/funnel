#!/usr/bin/env bash
set -Eeuo pipefail
workspace=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
state="$workspace/.gen3-local/arm-runner"
mkdir -p "$state"
runner=funnel-gen3-arm-runner
image=funnel-gen3-ci-runner:arm64
owner_label=io.calypr.funnel.gen3-workspace
[[ $# -gt 0 ]] || set -- ci
require_owner() {
  [[ "$(docker inspect --format '{{index .Config.Labels "io.calypr.funnel.gen3-workspace"}}' "$runner")" == "$workspace" ]] || {
    printf 'Refusing to use a runner owned by another checkout.\n' >&2; exit 2;
  }
}
case "$1" in
  build-runner)
    exec docker build --platform linux/arm64 -f "$workspace/tests/gen3/ci-runner/Dockerfile" -t "$image" "$workspace"
    ;;
  stop-runner)
    if docker container inspect "$runner" >/dev/null 2>&1; then
      require_owner
      docker stop --timeout 20 "$runner"
      docker rm "$runner"
    fi
    exit
    ;;
esac
docker image inspect "$image" >/dev/null 2>&1 || {
  printf 'Build the native ARM runner first: bash tests/gen3/docker-dev.sh build-runner\n' >&2; exit 2;
}
[[ $(docker info --format '{{.Architecture}}') == aarch64 ]] || {
  printf 'The local runner requires a native ARM64 Docker daemon.\n' >&2; exit 2;
}
if docker container inspect "$runner" >/dev/null 2>&1; then
  require_owner
else
  docker create --init --privileged --platform linux/arm64 --cpuset-cpus=0-3 --memory=16g --name "$runner" \
    --label "$owner_label=$workspace" \
    --mount type=volume,src=funnel-gen3-arm-docker,dst=/var/lib/docker \
    --mount type=volume,src=funnel-gen3-arm-workspace,dst=/workspace \
    --mount type=volume,src=funnel-gen3-arm-logs,dst=/var/log/gen3-ci \
    "$image" serve >/dev/null
fi
if [[ "$(docker inspect --format '{{.State.Running}}' "$runner")" != true ]]; then
  docker start "$runner" >/dev/null
fi
ready=false
readiness_deadline=$((SECONDS + 60))
while (( SECONDS < readiness_deadline )); do
  if docker exec "$runner" bash -c 'test -f /workspace/runner-ready && docker info >/dev/null 2>&1'; then ready=true; break; fi
  if [[ "$(docker inspect --format '{{.State.Running}}' "$runner")" != true ]]; then break; fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  docker logs --tail 40 "$runner" >&2
  printf 'Runner is not ready; its container and logs are retained.\n' >&2
  exit 1
fi
[[ "$1" != up ]] || exit 0
head=$(git -C "$workspace" rev-parse HEAD)
repository="$state/repository-$head.bundle"
if [[ ! -f "$repository" ]]; then git -C "$workspace" bundle create "$repository" HEAD; fi
if ! docker exec "$runner" test -f "/workspace/repository-$head.bundle"; then
  docker cp "$repository" "$runner:/workspace/repository-$head.bundle"
fi
package=$(mktemp "$state/source-XXXXXX")
trap 'rm -f "$package"' EXIT
python3 "$workspace/tests/gen3/ci-runner/snapshot-source.py" "$workspace" "$package"
archive_sha=$(shasum -a 256 "$package" | awk '{print $1}')
remote_package="/workspace/$(basename "$package").tar"
remote_sync="/workspace/$(basename "$package")-sync.py"
docker cp "$package" "$runner:$remote_package"
docker cp "$workspace/tests/gen3/ci-runner/sync-snapshot.py" "$runner:$remote_sync"
docker exec -i --env RUNNING_LOCAL="${RUNNING_LOCAL:-}" "$runner" bash -s -- "$remote_package" "/workspace/repository-$head.bundle" "$remote_sync" "$archive_sha" "$@" <<'LINUX'
set -Eeuo pipefail
[[ $(uname -m) == aarch64 ]]
export FUNNEL_CI_STATE_DIR=/workspace/funnel/.gen3-local/docker-runner
export FUNNEL_CI_KIND_CLUSTER_NAME=funnel-gen3-arm
printf '%s  %s\n' "$4" "$1" | sha256sum --check
exec flock -n -E 75 --close /workspace/command.lock bash -c '
  set -Eeuo pipefail
  python3 "$3" "$1" /workspace/funnel "$2"
  rm -f "$1" "$3"
  shift 4
  cd /workspace/funnel
  if [[ "$1" == shell ]]; then shift; exec bash "$@"; fi
  exec bash tests/gen3/dev.sh "$@"
' bash "$@"
LINUX
