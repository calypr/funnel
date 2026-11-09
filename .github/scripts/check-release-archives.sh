#!/usr/bin/env bash
set -euo pipefail
release_dir=${1:-build/release}
release_dir=$(cd "$release_dir" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT
shopt -s nullglob
checksums=("$release_dir/"*checksums.txt)
archives=("$release_dir/"*.tar.gz)
if [[ ${#checksums[@]} != 1 || ${#archives[@]} != 4 ]]; then
  echo 'Expected one checksum file and four release archives' >&2
  exit 1
fi
(cd "$release_dir" && shasum -a 256 -c "${checksums[0]}")
for archive in "${archives[@]}"; do
  tar -tzf "$archive" > "$work_dir/contents"
  for required_file in funnel License.txt docs/upgrading.md; do
    if ! grep -Fxq "$required_file" "$work_dir/contents"; then
      echo "$archive is missing $required_file" >&2
      exit 1
    fi
  done
done
native_os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in
  arm64|aarch64) native_arch=arm64 ;;
  x86_64) native_arch=amd64 ;;
  *) echo 'Unsupported smoke-test architecture' >&2; exit 1 ;;
esac
native_archives=("$release_dir/funnel-$native_os-$native_arch-"*.tar.gz)
[[ ${#native_archives[@]} == 1 ]]
tar -xzf "${native_archives[0]}" -C "$work_dir"
"$work_dir/funnel" version | tee "$work_dir/version"
grep -Eq '^version: +[^ ]+' "$work_dir/version"
if grep -Eq '^version: +(unknown)?$' "$work_dir/version"; then
  echo 'Release binary is missing its version' >&2
  exit 1
fi
expected_commit=$(git rev-parse --short=7 HEAD)
grep -Eq "^git commit: +$expected_commit" "$work_dir/version"
"$work_dir/funnel" --help > /dev/null
"$work_dir/funnel" server --help > /dev/null
"$work_dir/funnel" task --help > /dev/null
