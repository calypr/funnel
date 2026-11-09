#!/usr/bin/env bash
set -euo pipefail
validator=$(cd "$(dirname "$0")" && pwd)/check-release-tag.sh
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT
cd "$work_dir"
git init -q
git config user.name 'Release test'
git config user.email 'release@example.invalid'
git -c commit.gpgsign=false commit --allow-empty -qm first
git -c tag.gpgsign=false tag v0.14.0-rc.1
"$validator" v0.14.0-rc.1
for invalid_tag in main v01.2.3 'v0.14.0;echo invalid' v0.14.0; do
  if "$validator" "$invalid_tag" > /dev/null 2>&1; then
    echo "Incorrectly accepted invalid or missing tag: $invalid_tag" >&2
    exit 1
  fi
done
git -c commit.gpgsign=false commit --allow-empty -qm second
if "$validator" v0.14.0-rc.1 > /dev/null 2>&1; then
  echo 'Incorrectly accepted a tag on a different commit' >&2
  exit 1
fi
echo 'Release tag validation passed'
