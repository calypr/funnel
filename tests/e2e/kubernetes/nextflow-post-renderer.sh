#!/usr/bin/env bash
set -Eeuo pipefail
fixture_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
workspace=${GITHUB_WORKSPACE:-$(cd "$fixture_dir/../../.." && pwd)}
export FUNNEL_CI_NEXTFLOW_WORKSPACE="$fixture_dir/nextflow-workspace.yaml"
bash "$workspace/tests/e2e/gen3/gen3-post-renderer.sh" | yq eval '
  (select(.kind == "ConfigMap" and .metadata.name == "funnel-server-config") |
    .data."funnel-server.yaml" |= (from_yaml |
      .Worker.WorkDir = "/opt/funnel/funnel-work-dir" |
      .Kubernetes.Storage = load(strenv(FUNNEL_CI_NEXTFLOW_WORKSPACE)) |
      .Kubernetes.PVTemplate = "" |
      .Kubernetes.PVCTemplate = "" |
      to_yaml)) // .
' -
