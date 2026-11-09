#!/usr/bin/env python3
"""Report new unreachable Go functions without deleting code."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
BASELINE = ROOT / ".github/deadcode-baseline.txt"
TOOL = "golang.org/x/tools/cmd/deadcode@v0.52.0"


def analyze():
    build_env = os.environ.copy()
    for key in ("GOOS", "GOARCH"):
        build_env.pop(key, None)
    build_env["GOTOOLCHAIN"] = subprocess.check_output(
        ["go", "env", "GOVERSION"], cwd=ROOT, env=build_env, text=True
    ).strip()
    with tempfile.TemporaryDirectory(prefix="funnel-deadcode-") as directory:
        build_env["GOBIN"] = directory
        subprocess.run(["go", "install", TOOL], cwd=ROOT, env=build_env, check=True)
        analysis_env = build_env | {"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0"}
        binary = Path(directory) / ("deadcode.exe" if os.name == "nt" else "deadcode")
        output = subprocess.check_output(
            [str(binary), "-json", "-test", "./..."],
            cwd=ROOT,
            env=analysis_env,
            text=True,
        )
    return {
        f"{package['Path']}.{function['Name']}": function["Position"]
        for package in (json.loads(output or "[]") or [])
        for function in package["Funcs"]
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--update-baseline", action="store_true")
    args = parser.parse_args()
    try:
        findings = analyze()
    except subprocess.CalledProcessError as error:
        return error.returncode
    if args.update_baseline:
        BASELINE.write_text("".join(f"{symbol}\n" for symbol in sorted(findings)))
        print(f"Recorded {len(findings)} findings. Review the baseline diff before committing.")
        return 0
    expected = set(BASELINE.read_text().splitlines())
    added = findings.keys() - expected
    resolved = expected - findings.keys()
    if added:
        print("New unreachable functions need review:", file=sys.stderr)
        for symbol in sorted(added):
            position = findings[symbol]
            print(f"{position['File']}:{position['Line']}: {symbol}", file=sys.stderr)
        print("Wire up intended behavior, remove obsolete code, or document an intentional baseline addition.", file=sys.stderr)
    if resolved:
        print("Findings no longer reported; remove them from the baseline:", file=sys.stderr)
        print("\n".join(sorted(resolved)), file=sys.stderr)
    if added or resolved:
        return 1
    print(f"Dead-code check passed: no new findings; {len(expected)} existing findings recorded.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
