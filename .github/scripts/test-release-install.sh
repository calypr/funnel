#!/usr/bin/env bash
set -euo pipefail
repo_dir=$(cd "$(dirname "$0")/../.." && pwd)
release_dir=${1:-$repo_dir/build/release}
release_dir=$(cd "$release_dir" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT
metadata="$work_dir/release.json"
{
  printf '{\n"tag_name": "snapshot",\n"assets": [\n'
  separator=''
  for asset in "$release_dir/"*.tar.gz "$release_dir/"*checksums.txt; do
    printf '%s{"browser_download_url": "file://%s"}' "$separator" "$asset"
    separator=$',\n'
  done
  printf '\n]}\n'
} > "$metadata"
export RELEASE_TEST_METADATA="$metadata"
# Serve metadata locally; real curl still downloads the actual built archives.
curl() {
  case "${*: -1}" in
    https://api.github.com/repos/calypr/funnel/releases*) cat "$RELEASE_TEST_METADATA" ;;
    *) command curl "$@" ;;
  esac
}
export -f curl
cd "$work_dir"
bash "$repo_dir/install.sh" snapshot "install destination"
"$work_dir/install destination/funnel" version
