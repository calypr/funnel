#!/usr/bin/env bash
set -Eeuo pipefail
script_dir=$(cd "$(dirname "$0")" && pwd)
architecture=${FUNNEL_CI_BUILD_PLATFORM#linux/}
while read -r deployment executable; do
  kubectl -n "$NAMESPACE" exec "deployment/$deployment" -- /bin/sh -c '
    executable=$1
    if [ "${executable#/}" = "$executable" ]; then executable=$(command -v "$executable"); fi
    head -c20 "$executable"
  ' sh "$executable" | python3 "$script_dir/verify-elf.py" "$architecture" "$deployment:$executable"
done <<'EXECUTABLES'
arborist-deployment /go/src/github.com/uc-cdis/arborist/bin/arborist
indexd-deployment python3
fence-deployment python3
presigned-url-fence-deployment python3
gen3-workflow-deployment python3
gen3-workflow-dpop-deployment python3
revproxy-deployment nginx
EXECUTABLES
