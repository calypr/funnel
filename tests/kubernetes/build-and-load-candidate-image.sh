#!/usr/bin/env bash
set -Eeuo pipefail

: "${GITHUB_WORKSPACE:?}"
: "${GITHUB_ENV:?}"

workspace=$(cd "$GITHUB_WORKSPACE" && pwd)
PYTHONDONTWRITEBYTECODE=1 python3 "$workspace/tests/test-image-config-digest.py"
source_sha=$(git -C "$workspace" rev-parse HEAD)
candidate_image="quay.io/ohsu-comp-bio/funnel:ci-${source_sha:0:12}"

printf 'source_commit=%s\ncandidate_image=%s\n' "$source_sha" "$candidate_image"
DOCKER_BUILDKIT=1 docker build \
  --platform linux/amd64 \
  --label "org.opencontainers.image.revision=$source_sha" \
  --tag "$candidate_image" \
  "$workspace"

candidate_image_id=$(docker image inspect "$candidate_image" --format '{{.Id}}')
candidate_revision=$(docker image inspect "$candidate_image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')
if [[ "$candidate_revision" != "$source_sha" ]]; then
  printf 'candidate image revision %s does not match checked-out commit %s\n' "$candidate_revision" "$source_sha" >&2
  exit 1
fi

archive_dir=$(mktemp -d)
trap 'rm -rf "$archive_dir"' EXIT
image_archive="$archive_dir/candidate.tar"
docker save "$candidate_image" >"$image_archive"
candidate_config_id=$(python3 "$workspace/tests/image-config-digest.py" "$image_archive" "$candidate_image")
printf 'candidate Docker ID=%s archived config=%s\n' "$candidate_image_id" "$candidate_config_id"

nodes=$(kind get nodes --name funnel-gen3)
if [[ -z "$nodes" ]]; then
  printf 'Kind cluster funnel-gen3 has no nodes\n' >&2
  exit 1
fi

while IFS= read -r node; do
  [[ -n "$node" ]] || continue
  docker exec -i "$node" ctr --namespace k8s.io images import --snapshotter overlayfs - <"$image_archive"
  node_config_id=$(docker exec "$node" crictl inspecti "$candidate_image" | jq -er '.status.id')
  if [[ "$node_config_id" != "$candidate_config_id" ]]; then
    printf 'node %s loaded image ID %s, expected %s\n' "$node" "$node_config_id" "$candidate_config_id" >&2
    exit 1
  fi
  printf 'node=%s image=%s image_id=%s\n' "$node" "$candidate_image" "$node_config_id"
done <<<"$nodes"

{
  printf 'FUNNEL_CI_IMAGE_REF=%s\n' "$candidate_image"
  printf 'FUNNEL_CI_IMAGE_ID=%s\n' "$candidate_image_id"
  printf 'FUNNEL_CI_SOURCE_SHA=%s\n' "$source_sha"
} >>"$GITHUB_ENV"
