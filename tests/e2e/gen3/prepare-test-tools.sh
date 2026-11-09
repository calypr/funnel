#!/usr/bin/env bash
set -Eeuo pipefail
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
: "${FUNNEL_CI_BUILD_PLATFORM:?build platform is required}"
: "${FUNNEL_CI_KIND_CLUSTER_NAME:?owned cluster is required}"
: "${FUNNEL_CI_STATE_DIR:?state directory is required}"
architecture=${FUNNEL_CI_BUILD_PLATFORM#linux/}
archive_root=${FUNNEL_CI_TEST_IMAGE_CACHE_DIR:-$FUNNEL_CI_STATE_DIR/test-image-archives}
mkdir -p "$archive_root/$architecture"
context=$(mktemp -d "$FUNNEL_CI_STATE_DIR/test-tools-XXXXXX")
trap 'rm -rf "$context"' EXIT
for fixture in arborist awshelper dicom canary; do
  recipe_dir="$script_dir/$fixture-image"
  case "$fixture" in
    arborist) image=funnel-gen3-arborist:ci; executable=/go/src/github.com/uc-cdis/arborist/bin/arborist ;;
    awshelper) image=funnel-gen3-awshelper:ci; executable=/usr/local/aws-cli/v2/current/dist/aws ;;
    dicom) image=quay.io/cdis/gen3-workflow:integration_tests_dicom_image; executable=/usr/local/bin/python3.11 ;;
    canary) image=quay.io/biocontainers/ubuntu:24.04; executable=/bin/bash ;;
  esac
  recipe=$(python3 - "$recipe_dir" <<'PYTHON'
import hashlib,pathlib,sys
root=pathlib.Path(sys.argv[1])
print(hashlib.sha256(b"".join(p.read_bytes() for p in sorted(root.iterdir()) if p.is_file())).hexdigest())
PYTHON
)
  archive="$archive_root/$architecture/$fixture-$recipe.tar"
  if [[ ! -f "$archive" ]]; then
    if ! docker image inspect "$image" 2>/dev/null | jq -e --arg architecture "$architecture" --arg recipe "$recipe" \
      '.[0] | .Architecture == $architecture and .Config.Labels["io.calypr.funnel.fixture-recipe"] == $recipe' >/dev/null; then
      mkdir -p "$context/$fixture"
      if [[ "$fixture" == dicom ]]; then
        python3 "$recipe_dir/extract-source.py" "$context/$fixture"
      fi
      docker build --platform "$FUNNEL_CI_BUILD_PLATFORM" \
        --file "$recipe_dir/Dockerfile" \
        --label "io.calypr.funnel.fixture-recipe=$recipe" --tag "$image" "$context/$fixture"
    fi
    docker image inspect "$image" | jq -e --arg architecture "$architecture" '.[0].Architecture == $architecture' >/dev/null
    docker save "$image" >"$context/image.tar"
    mv "$context/image.tar" "$archive"
    printf 'Saved native fixture archive: %s\n' "$archive"
  else
    printf 'Reusing native fixture archive: %s\n' "$archive"
  fi
  nodes=("$FUNNEL_CI_KIND_CLUSTER_NAME-control-plane" "$FUNNEL_CI_KIND_CLUSTER_NAME-worker")
  if [[ "$fixture" == awshelper || "$fixture" == arborist ]]; then
    # Gen3 services cannot schedule onto Kind's tainted control plane.
    kubectl get node "$FUNNEL_CI_KIND_CLUSTER_NAME-control-plane" -o json | \
      jq -e '.spec.taints | any(.key == "node-role.kubernetes.io/control-plane" and .effect == "NoSchedule")' >/dev/null
    nodes=("$FUNNEL_CI_KIND_CLUSTER_NAME-worker")
  fi
  for node in "${nodes[@]}"; do
    runtime_image=$image
    if [[ "$image" != */* ]]; then runtime_image="docker.io/library/$image"; fi
    docker exec -i "$node" ctr -n k8s.io images import \
      --label io.cri-containerd.image=managed --label io.cri-containerd.pinned=pinned \
      - <"$archive" >/dev/null
    image_status="$context/image-status.json"
    image_ready=false
    for ((attempt=0; attempt<100; attempt++)); do
      if docker exec "$node" crictl inspecti "$runtime_image" >"$image_status" 2>"$context/image-status.err"; then
        image_ready=true
        break
      fi
      sleep 0.1
    done
    if [[ "$image_ready" != true ]]; then
      cat "$context/image-status.err" >&2
      printf 'Imported native fixture is not visible through CRI: %s on %s\n' "$runtime_image" "$node" >&2
      exit 2
    fi
    jq -e --arg architecture "$architecture" --arg recipe "$recipe" \
      '.status.pinned == true and (.info.imageSpec | .architecture == $architecture and .config.Labels["io.calypr.funnel.fixture-recipe"] == $recipe)' \
      "$image_status" >/dev/null
    executables=("$executable")
    if [[ "$fixture" == dicom ]]; then executables+=(/usr/bin/bash); fi
    for executable_path in "${executables[@]}"; do
      docker exec "$node" ctr -n k8s.io run --rm "$runtime_image" "gen3-elf-$fixture-$$" \
        /bin/sh -c 'head -c20 "$1"' sh "$executable_path" | \
        python3 "$script_dir/verify-elf.py" "$architecture" "$image:$executable_path"
    done
    printf 'Native test image loaded: image=%s node=%s architecture=%s recipe=%s\n' "$image" "$node" "$architecture" "$recipe"
  done
  if [[ "$fixture" == arborist && -n "${FUNNEL_CI_ARBORIST_READY_FILE:-}" ]]; then
    touch "$FUNNEL_CI_ARBORIST_READY_FILE"
  fi
done

if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  printf 'test_images_ready=true\n' >>"$GITHUB_OUTPUT"
fi
