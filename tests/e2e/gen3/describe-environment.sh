#!/usr/bin/env bash
set -Eeuo pipefail
printf 'host: '; uname -srm
printf 'build_platform=%s\n' "$FUNNEL_CI_BUILD_PLATFORM"
printf 'fresh_fixture=%s\n' "${FUNNEL_CI_FRESH_FIXTURE:-true}"
java -version
python --version
uv --version
kind --version
helm version --short
kubectl version --client -o json
docker version --format 'docker_client={{.Client.Version}} docker_server={{.Server.Version}}'
docker info --format 'docker_cpus={{.NCPU}} docker_memory_bytes={{.MemTotal}} docker_storage={{.Driver}} docker_driver_status={{json .DriverStatus}}'
