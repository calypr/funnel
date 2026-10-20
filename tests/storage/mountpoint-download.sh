#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/../.." && pwd)
scratch=$(mktemp -d "${TMPDIR:-/tmp}/funnel-mountpoint.XXXXXX")
network=
minio=
mountpoint=
cleanup() {
  set +e
  if [[ -n "$mountpoint" ]]; then docker rm -f "$mountpoint" >/dev/null; fi
  if [[ -n "$minio" ]]; then docker rm -f "$minio" >/dev/null; fi
  if [[ -n "$network" ]]; then docker network rm "$network" >/dev/null; fi
  rm -rf "$scratch"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

arch=$(docker version --format '{{.Server.Arch}}')
case "$arch" in
  arm64) package_arch=arm64 ;;
  amd64) package_arch=x86_64 ;;
  *) echo "Unsupported Docker architecture: $arch" >&2; exit 1 ;;
esac
version=${MOUNTPOINT_VERSION:-1.24.0}
curl -fsSL "https://s3.amazonaws.com/mountpoint-s3-release/$version/$package_arch/mount-s3-$version-$package_arch.deb" -o "$scratch/mount-s3.deb"
cd "$repo"
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go test -c ./storage -o "$scratch/storage.test"

network=$(docker network create "funnel-mountpoint-$(basename "$scratch")")
minio=$(docker run -d --network "$network" --network-alias minio \
  -e MINIO_ROOT_USER=funnel-test -e MINIO_ROOT_PASSWORD=funnel-test-secret \
  quay.io/minio/minio:RELEASE.2025-03-12T18-04-18Z server /data)
docker run --rm --network "$network" --entrypoint /bin/sh \
  bitnamilegacy/minio-client:2025.7.21-debian-12-r3 -c '
    attempt=0
    until mc alias set test http://minio:9000 funnel-test funnel-test-secret; do
      attempt=$((attempt + 1))
      [ "$attempt" -lt 30 ] || exit 1
      sleep 1
    done
    mc mb test/mounted test/sources &&
    printf marker | mc pipe test/mounted/subpath/marker
  '
mountpoint=$(docker run -d --network "$network" \
  --cap-add SYS_ADMIN --device /dev/fuse --security-opt apparmor=unconfined \
  -e AWS_ACCESS_KEY_ID=funnel-test -e AWS_SECRET_ACCESS_KEY=funnel-test-secret \
  "${MOUNTPOINT_TEST_IMAGE:-debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587}" sleep infinity)
docker cp "$scratch/mount-s3.deb" "$mountpoint:/tmp/mount-s3.deb"
docker cp "$scratch/storage.test" "$mountpoint:/tmp/storage.test"
docker exec "$mountpoint" sh -ec '
  apt-get update -qq
  apt-get install -y -qq /tmp/mount-s3.deb
  mkdir -p /mnt /bound
  mount-s3 --version
  mount-s3 mounted /mnt --endpoint-url http://minio:9000 --region us-east-1 \
    --force-path-style --allow-overwrite --allow-delete --metadata-ttl minimal \
    --write-part-size 5242880 --read-part-size 5242880
  mount --bind /mnt/subpath /bound
  cat /proc/self/mountinfo
'
docker exec -e FUNNEL_MOUNTPOINT_TEST_DIR=/mnt \
  -e FUNNEL_MOUNTPOINT_TEST_ENDPOINT=http://minio:9000 "$mountpoint" \
  /tmp/storage.test -test.v \
  -test.run '^(TestDownloadMountpoint|TestMountpoint|TestGenericS3Download|TestWriteDownloaded|TestWriteRootDownloaded|TestWriteRootFileAtomically)'
docker exec -e FUNNEL_MOUNTPOINT_TEST_DIR=/bound \
  -e FUNNEL_MOUNTPOINT_TEST_PREFIX=subpath/ \
  -e FUNNEL_MOUNTPOINT_TEST_ENDPOINT=http://minio:9000 "$mountpoint" \
  /tmp/storage.test -test.v -test.run '^TestDownloadMountpoint$'
