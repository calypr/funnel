#!/usr/bin/env bash
set -Eeuo pipefail
test_root=${1:?test root is required}
python_bin=$(command -v python)
uv tool run --python "$python_bin" --from poetry==2.2.1 poetry -C "$test_root" env use "$python_bin"
uv tool run --python "$python_bin" --from poetry==2.2.1 poetry -C "$test_root" install --only main --no-root --no-interaction

# The upstream Python wrapper polls forever when the launcher cannot start.
# Start it here so missing Java and runtime downloads fail before pytest.
timeout --signal=TERM --kill-after=3s 45s \
  uv tool run --python "$python_bin" --from poetry==2.2.1 poetry -C "$test_root" run nextflow -version
