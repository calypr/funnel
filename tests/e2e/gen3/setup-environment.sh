#!/usr/bin/env bash
set -Eeuo pipefail

# Install only the local object store and the exact Mountpoint CSI chart used by
# the integration test. Gen3 Helm setup lives in bootstrap-services.sh.
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
: "${NAMESPACE:?Kubernetes namespace is required}"
: "${KUBECONFIG:?an explicit owned-cluster kubeconfig is required}"
: "${FUNNEL_CI_STATE_DIR:?persistent local state directory is required}"
: "${TEST_REPO:?pinned Gen3 test checkout is required}"
helm_bin=${HELM_BIN:-helm}
kubectl_bin=${KUBECTL_BIN:-kubectl}
aws_bin=${AWS_BIN:-aws}
minio_image=${FUNNEL_CI_MINIO_IMAGE:-$(jq -er '.images["quay.io/cdis/minio:chainguard-latest-2026-09-25"].reference' "$script_dir/fixture-images.json")}

command -v "$helm_bin" >/dev/null || { printf 'helm is required\n' >&2; exit 2; }
command -v "$kubectl_bin" >/dev/null || { printf 'kubectl is required\n' >&2; exit 2; }
command -v "$aws_bin" >/dev/null || { printf 'aws CLI is required to seed the local Gen3 user fixture\n' >&2; exit 2; }
csi_images="$script_dir/csi-images.yaml"
if [[ "${FUNNEL_CI_BUILD_PLATFORM:-}" == linux/arm64 ]]; then csi_images="$script_dir/csi-images-arm64.yaml"; fi
[[ -f "$csi_images" ]] || { printf 'CSI image pins are missing\n' >&2; exit 2; }

export KUBECONFIG
state_dir=$(cd "$FUNNEL_CI_STATE_DIR" && pwd)
chart_dir="$state_dir/charts"
mkdir -p "$chart_dir" "$state_dir/helm"

"$kubectl_bin" create namespace "$NAMESPACE" --dry-run=client -o yaml | "$kubectl_bin" apply -f -
"$kubectl_bin" apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: minio
  namespace: ${NAMESPACE}
  labels:
    app: minio
spec:
  volumes:
    - name: data
      emptyDir:
        medium: Memory
        sizeLimit: 1Gi
  containers:
    - name: minio
      image: ${minio_image}
      args: [server, /data]
      volumeMounts:
        - name: data
          mountPath: /data
      env:
        - name: MINIO_ROOT_USER
          value: minioadmin
        - name: MINIO_ROOT_PASSWORD
          value: minioadmin
      ports:
        - containerPort: 9000
---
apiVersion: v1
kind: Service
metadata:
  name: minio
  namespace: ${NAMESPACE}
spec:
  selector:
    app: minio
  ports:
    - port: 9000
      targetPort: 9000
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: dev-minio-ingress-netpolicy
  namespace: ${NAMESPACE}
spec:
  podSelector:
    matchLabels:
      app: minio
  policyTypes:
  - Ingress
  ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: mount-s3
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: ${NAMESPACE}
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: workflow-pods-${NAMESPACE}
      podSelector:
        matchLabels:
          app: funnel-worker
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: dev-minio-egress-netpolicy
  namespace: ${NAMESPACE}
spec:
  podSelector: {}
  policyTypes:
    - Egress
  egress:
  - to:
    - podSelector:
        matchLabels:
          app: minio
EOF

"$kubectl_bin" -n kube-system create secret generic aws-secret \
  --from-literal=key_id=minioadmin \
  --from-literal=access_key=minioadmin \
  --from-literal=session_token= \
  --dry-run=client -o yaml | "$kubectl_bin" apply -f -

helm_root="$state_dir/helm${FUNNEL_CI_HELM_PROFILE:+/$FUNNEL_CI_HELM_PROFILE}"
helm_config="$helm_root/config"
helm_cache="$helm_root/cache"
helm_data="$helm_root/data"
mkdir -p "$helm_config" "$helm_cache" "$helm_data"
export HELM_CONFIG_HOME="$helm_config" HELM_CACHE_HOME="$helm_cache" HELM_DATA_HOME="$helm_data"
if ! "$helm_bin" status aws-mountpoint-s3-csi-driver -n kube-system >/dev/null 2>&1; then
  "$helm_bin" repo add aws-mountpoint-s3-csi-driver https://awslabs.github.io/mountpoint-s3-csi-driver >/dev/null
fi
if [[ ! -f "$chart_dir/aws-mountpoint-s3-csi-driver-2.8.0.tgz" ]]; then
  "$helm_bin" repo update aws-mountpoint-s3-csi-driver >/dev/null
  "$helm_bin" pull aws-mountpoint-s3-csi-driver/aws-mountpoint-s3-csi-driver \
    --version 2.8.0 --destination "$chart_dir"
fi
"$helm_bin" upgrade --install aws-mountpoint-s3-csi-driver \
  "$chart_dir/aws-mountpoint-s3-csi-driver-2.8.0.tgz" \
  --namespace kube-system --values "$csi_images" \
  --wait --timeout 180s

"$kubectl_bin" -n "$NAMESPACE" wait --for=condition=Ready pod/minio --timeout=90s
"$kubectl_bin" -n kube-system rollout status daemonset/s3-csi-node --timeout=120s

# Gen3 usersync reads the same pinned user.yaml from this local S3-compatible
# endpoint. Keep credentials in the process environment; never write ~/.aws.
"$kubectl_bin" -n "$NAMESPACE" port-forward service/minio 19000:9000 \
  >"$state_dir/minio-port-forward.log" 2>&1 &
port_forward_pid=$!
cleanup() { kill "$port_forward_pid" 2>/dev/null || true; wait "$port_forward_pid" 2>/dev/null || true; }
trap cleanup EXIT
ready=false
for _ in $(seq 1 40); do
  if ! kill -0 "$port_forward_pid" 2>/dev/null; then
    cat "$state_dir/minio-port-forward.log" >&2
    printf 'MinIO port-forward exited before becoming ready\n' >&2
    exit 1
  fi
  if curl --fail --silent --max-time 1 http://127.0.0.1:19000/minio/health/ready >/dev/null; then
    ready=true
    break
  fi
  sleep 0.25
done
[[ "$ready" == true ]] || { cat "$state_dir/minio-port-forward.log" >&2; exit 1; }
export AWS_ACCESS_KEY_ID=minioadmin AWS_SECRET_ACCESS_KEY=minioadmin
export AWS_DEFAULT_REGION=us-east-1 AWS_EC2_METADATA_DISABLED=true AWS_MAX_ATTEMPTS=1
export AWS_ENDPOINT_URL=http://127.0.0.1:19000
"$aws_bin" --endpoint-url "$AWS_ENDPOINT_URL" s3api head-bucket --bucket cdis-gen3-users >/dev/null 2>&1 || \
  "$aws_bin" s3 mb s3://cdis-gen3-users --endpoint-url "$AWS_ENDPOINT_URL" >/dev/null
"$aws_bin" s3 cp "$script_dir/user.yaml" \
  s3://cdis-gen3-users/ci/user.yaml --endpoint-url "$AWS_ENDPOINT_URL" --only-show-errors
printf 'MinIO and Mountpoint CSI 2.8.0 are ready; pinned users fixture uploaded\n'
