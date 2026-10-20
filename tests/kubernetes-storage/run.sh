#!/usr/bin/env bash
set -euo pipefail
umask 077

cd "$(dirname "$0")/../.."
output_dir="${FUNNEL_E2E_OUTPUT_DIR:-$(mktemp -d)}"
mkdir -p "$output_dir"
output_dir="$(cd "$output_dir" && pwd)"
export FUNNEL_E2E_OUTPUT_DIR="$output_dir"
cluster="funnel-storage-${RANDOM}"
export KUBECONFIG="$output_dir/kubeconfig"
export FUNNEL_E2E_IMAGE="funnel-storage-e2e:${cluster#funnel-storage-}"

cleanup() {
  status=$?
  trap - EXIT
  if [[ -s "$KUBECONFIG" ]]; then
    kubectl get pods,pvc,jobs -A -o wide > "$output_dir/resources.txt" 2>&1 || true
    kubectl get events -A --sort-by='.lastTimestamp' > "$output_dir/events.txt" 2>&1 || true
    kubectl get pv -o yaml > "$output_dir/pvs.yaml" 2>&1 || true
    while read -r pod; do
      kubectl -n funnel-storage-e2e logs "$pod" --all-containers > "$output_dir/${pod}.log" 2>&1 || true
    done < <(kubectl -n funnel-storage-e2e get pods -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null)
  fi
  k3d cluster delete "$cluster" || true
  docker image rm "$FUNNEL_E2E_IMAGE" >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT

arch="$(docker info --format '{{.Architecture}}')"
case "$arch" in
  aarch64|arm64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) echo "Unsupported Docker architecture: $arch" >&2; exit 1 ;;
esac
mkdir -p "$output_dir/image"
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$output_dir/image/funnel" .
docker build -t "$FUNNEL_E2E_IMAGE" -f tests/kubernetes-storage/Dockerfile "$output_dir/image"
k3d cluster create "$cluster" --image rancher/k3s:v1.31.5-k3s1 --wait \
  --k3s-arg '--kubelet-arg=eviction-hard=nodefs.available<1Gi,imagefs.available<1Gi,nodefs.inodesFree<1%,imagefs.inodesFree<1%@server:0' \
  --k3s-arg '--kubelet-arg=image-gc-high-threshold=99@server:0' \
  --k3s-arg '--kubelet-arg=image-gc-low-threshold=98@server:0' \
  --kubeconfig-update-default=false --kubeconfig-switch-context=false
k3d kubeconfig get "$cluster" > "$KUBECONFIG"
k3d image import "$FUNNEL_E2E_IMAGE" -c "$cluster"
test "$(kubectl get storageclass local-path -o jsonpath='{.provisioner}')" = rancher.io/local-path
test "$(kubectl get storageclass local-path -o jsonpath='{.metadata.annotations.storageclass\.kubernetes\.io/is-default-class}')" = true
kubectl apply -f tests/kubernetes-storage/native-storageclass.yaml
FUNNEL_KUBERNETES_TEST_KUBECONFIG="$KUBECONFIG" \
  go test -run '^TestNativeStorage(Permissions)?Live$' -v -count=1 -timeout=10m ./compute/kubernetes \
  2>&1 | tee "$output_dir/native-storage.log"
kubectl create namespace funnel-storage-e2e
kubectl -n funnel-storage-e2e apply -f tests/kubernetes-storage/minio.yaml
kubectl -n funnel-storage-e2e rollout status deployment/minio --timeout=180s
python3 -u tests/kubernetes-storage/test_storage.py "$@" 2>&1 | tee "$output_dir/tests.log"
