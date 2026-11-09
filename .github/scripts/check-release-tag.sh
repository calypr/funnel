#!/usr/bin/env bash
set -euo pipefail
release_tag=${1:?Supply a version tag}
if [[ ! "$release_tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "Expected a version tag such as v0.14.0 or v0.14.0-rc.1" >&2
  exit 1
fi
if [[ $(git rev-parse "refs/tags/$release_tag^{commit}") != $(git rev-parse HEAD) ]]; then
  echo "Release tag $release_tag does not point to the checked-out commit" >&2
  exit 1
fi
