#!/usr/bin/env python3
"""Run a Gen3 command with redacted output and an optional deadline (0 disables it)."""

from __future__ import annotations

import os
import selectors
import signal
import subprocess
import sys
import time
from pathlib import Path
from typing import TextIO

from redact_logs import redact


def emit(data: bytes, artifact: TextIO) -> None:
    text = redact(data.decode("utf-8", errors="replace"))
    sys.stdout.write(text)
    sys.stdout.flush()
    artifact.write(text)
    artifact.flush()


def next_record_end(data: bytearray) -> int | None:
    separators = [position for marker in (b"\n", b"\r") if (position := data.find(marker)) >= 0]
    return min(separators) + 1 if separators else None


def run(
    working_dir: Path,
    log_path: Path,
    max_seconds: int,
    command: list[str],
    append_log: bool,
) -> int:
    environment = os.environ.copy()
    environment["PYTHONUNBUFFERED"] = "1"
    requested_signal: int | None = None

    def request_shutdown(signum: int, _frame: object) -> None:
        nonlocal requested_signal
        requested_signal = signum

    signal.signal(signal.SIGINT, request_shutdown)
    signal.signal(signal.SIGTERM, request_shutdown)

    try:
        process = subprocess.Popen(
            command,
            cwd=working_dir,
            env=environment,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            bufsize=0,
            start_new_session=True,
        )
    except OSError as error:
        message = f"could not start Gen3 command: {error.strerror or error}\n"
        sys.stdout.write(message)
        sys.stdout.flush()
        with log_path.open("a" if append_log else "w", encoding="utf-8", errors="replace") as artifact:
            artifact.write(message)
        return 127

    assert process.stdout is not None
    selector = selectors.DefaultSelector()
    selector.register(process.stdout, selectors.EVENT_READ)
    pending = bytearray()
    deadline = time.monotonic() + max_seconds if max_seconds else float("inf")
    stop_reason: str | None = None
    shutdown_deadline: float | None = None
    force_close_deadline: float | None = None

    def signal_test_group(signum: int) -> None:
        try:
            os.killpg(process.pid, signum)
        except ProcessLookupError:
            if process.poll() is None:
                process.send_signal(signum)
        except PermissionError:
            if process.poll() is None:
                process.send_signal(signum)

    try:
        with log_path.open("a" if append_log else "w", encoding="utf-8", errors="replace") as artifact:
            while selector.get_map():
                now = time.monotonic()
                if requested_signal is not None and stop_reason is None:
                    stop_reason = "signal"
                    signal_test_group(signal.SIGTERM)
                    shutdown_deadline = now + 2
                elif now >= deadline and stop_reason is None:
                    stop_reason = "timeout"
                    signal_test_group(signal.SIGTERM)
                    shutdown_deadline = now + 2

                if shutdown_deadline is not None and now >= shutdown_deadline:
                    signal_test_group(signal.SIGKILL)
                    shutdown_deadline = None
                    force_close_deadline = now + 2
                if force_close_deadline is not None and now >= force_close_deadline:
                    selector.unregister(process.stdout)
                    process.stdout.close()
                    break

                wait_for = 0.1
                if stop_reason is None:
                    wait_for = min(wait_for, max(0, deadline - now))
                elif shutdown_deadline is not None:
                    wait_for = min(wait_for, max(0, shutdown_deadline - now))

                for key, _ in selector.select(wait_for):
                    chunk = os.read(key.fd, 65536)
                    if not chunk:
                        selector.unregister(key.fileobj)
                        while pending:
                            end = next_record_end(pending) or len(pending)
                            emit(bytes(pending[:end]), artifact)
                            del pending[:end]
                        continue

                    pending.extend(chunk)
                    while (end := next_record_end(pending)) is not None:
                        emit(bytes(pending[:end]), artifact)
                        del pending[:end]

            while pending:
                end = next_record_end(pending) or len(pending)
                emit(bytes(pending[:end]), artifact)
                del pending[:end]

            if max_seconds == 0:
                while process.poll() is None and requested_signal is None:
                    time.sleep(0.1)

            if process.poll() is None:
                if requested_signal is not None and stop_reason is None:
                    stop_reason = "signal"
                elif time.monotonic() >= deadline and stop_reason is None:
                    stop_reason = "timeout"

                if stop_reason is not None:
                    signal_test_group(signal.SIGTERM)
                    try:
                        return_code = process.wait(timeout=2)
                    except subprocess.TimeoutExpired:
                        signal_test_group(signal.SIGKILL)
                        return_code = process.wait()
                else:
                    try:
                        wait_timeout = None if max_seconds == 0 else max(0, deadline - time.monotonic())
                        return_code = process.wait(timeout=wait_timeout)
                    except subprocess.TimeoutExpired:
                        stop_reason = "timeout"
                        signal_test_group(signal.SIGTERM)
                        try:
                            return_code = process.wait(timeout=2)
                        except subprocess.TimeoutExpired:
                            signal_test_group(signal.SIGKILL)
                            return_code = process.wait()
            else:
                return_code = process.wait()
    finally:
        selector.close()
        if process.poll() is None:
            try:
                signal_test_group(signal.SIGTERM)
                process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                signal_test_group(signal.SIGKILL)
                process.wait()
        if process.stdout and not process.stdout.closed:
            process.stdout.close()

    if stop_reason == "timeout":
        message = f"Gen3 command exceeded its {max_seconds}s limit\n"
        sys.stdout.write(message)
        sys.stdout.flush()
        with log_path.open("a", encoding="utf-8", errors="replace") as artifact:
            artifact.write(message)
        return 124
    if stop_reason == "signal":
        return 128 + (requested_signal or signal.SIGTERM)
    if return_code < 0:
        return 128 + -return_code
    return return_code


def main(arguments: list[str]) -> int:
    if len(arguments) < 4:
        print(
            f"usage: {Path(sys.argv[0]).name} <working-dir> <log-path> <max-seconds> <command> [args...]",
            file=sys.stderr,
        )
        return 2

    working_dir = Path(arguments[0]).resolve()
    log_path = Path(arguments[1]).resolve()
    log_path.parent.mkdir(parents=True, exist_ok=True)
    append_value = os.environ.pop("GEN3_LOG_APPEND", "0")
    if append_value not in {"0", "1"}:
        print("GEN3_LOG_APPEND must be 0 or 1", file=sys.stderr)
        return 2
    try:
        max_seconds = int(arguments[2])
    except ValueError:
        print("max-seconds must be an integer", file=sys.stderr)
        return 2
    if max_seconds < 0:
        print("max-seconds must be nonnegative (0 disables the deadline)", file=sys.stderr)
        return 2

    return run(working_dir, log_path, max_seconds, arguments[3:], append_value == "1")


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
