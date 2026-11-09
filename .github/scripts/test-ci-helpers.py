#!/usr/bin/env python3
"""Exercise CI download retries without network access or a Docker daemon."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPTS = Path(__file__).resolve().parent


class CIHelpersTest(unittest.TestCase):
    def test_image_downloads(self):
        for mode, calls_expected, success in (
            ("success", 2, True),
            ("retry", 3, True),
            ("fail", 3, False),
        ):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temp:
                directory = Path(temp)
                calls = directory / "calls"
                docker = directory / "docker"
                docker.write_text(
                    '#!/bin/sh\n'
                    'printf "%s\\n" "$*" >> "$CALLS"\n'
                    'n=$(wc -l < "$CALLS")\n'
                    'if [ "$MODE" = retry ] && [ "$n" -lt 2 ]; then exit 1; fi\n'
                    'if [ "$MODE" = fail ]; then exit 1; fi\n'
                    'exit 0\n'
                )
                docker.chmod(0o755)
                sleep = directory / "sleep"
                sleep.write_text("#!/bin/sh\nexit 0\n")
                sleep.chmod(0o755)
                env = {
                    **os.environ,
                    "PATH": str(directory) + os.pathsep + os.environ["PATH"],
                    "MODE": mode,
                    "CALLS": str(calls),
                }
                result = subprocess.run(
                    ["bash", str(SCRIPTS / "pull-test-images.sh"), "fixture:a", "fixture:b"],
                    env=env, capture_output=True, text=True, check=False,
                )
                self.assertEqual(result.returncode == 0, success, result.stderr)
                commands = calls.read_text().splitlines()
                self.assertEqual(len(commands), calls_expected)
                self.assertTrue(all(command.startswith("pull fixture:") for command in commands))

    def test_diagnostics_with_unavailable_docker(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            docker = directory / "docker"
            docker.write_text("#!/bin/sh\nexit 1\n")
            docker.chmod(0o755)
            subprocess.run(
                ["bash", str(SCRIPTS / "docker-diagnostics.sh"), str(directory / "output")],
                env={**os.environ, "PATH": str(directory) + os.pathsep + os.environ["PATH"]},
                check=True,
            )
            self.assertTrue((directory / "output/info.txt").exists())


if __name__ == "__main__":
    unittest.main()
