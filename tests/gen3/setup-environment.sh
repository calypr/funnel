#!/usr/bin/env bash
set -Eeuo pipefail

setup_script=$(mktemp)
cleanup() {
  status=$?
  trap - EXIT
  rm -f "$setup_script"
  if (( status != 0 )); then
    kubectl get nodes -o wide || true
    kubectl get pods -A -o wide || true
    kubectl get events -A --sort-by='.lastTimestamp' || true
    while read -r pod; do
      kubectl -n kube-system describe "$pod" || true
      kubectl -n kube-system logs "$pod" --all-containers --prefix --tail=100 || true
      kubectl -n kube-system logs "$pod" --all-containers --prefix --previous --tail=100 || true
    done < <(kubectl -n kube-system get pods -o name | awk '/\/s3-csi-/')
  fi
  exit "$status"
}
trap cleanup EXIT

curl --fail --silent --show-error --location \
  https://raw.githubusercontent.com/uc-cdis/gen3-workflow/500b11d7b132518522480fda9abaa1c5cd9a73e2/.github/workflows/integration_tests_on_kind/ci_override_config_and_start_minio.sh \
  --output "$setup_script"
bash "$setup_script"
