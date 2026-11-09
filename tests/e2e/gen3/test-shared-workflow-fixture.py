#!/usr/bin/env python3
"""Check cross-process storage initialization and failure propagation."""

import multiprocessing
import os
from pathlib import Path
import tempfile
import time

from funnel_ci_fixture import initialize_storage


class Client:
    def __init__(self, root, fail=False):
        self.root = Path(root)
        self.fail = fail

    def record(self, text):
        with (self.root / "events").open("a") as events:
            events.write(text + "\n")

    def setup_storage(self, user, expected_status):
        assert expected_status == 200
        self.record("setup " + user)
        if self.fail:
            raise AssertionError("Expected 200, got 403")
        time.sleep(0.01)
        return {"bucket": "test-bucket", "region": "us-east-1"}

    def cleanup_user_bucket(self, user):
        self.record("cleanup " + user)


def worker(root, barrier, results):
    os.environ["FUNNEL_CI_ARTIFACTS_DIR"] = root
    client = Client(root)
    barrier.wait(timeout=10)
    storage = initialize_storage(client, "main_account", "user0_account")
    client.record("test-start")
    results.put(storage)


def main():
    context = multiprocessing.get_context("spawn")
    with tempfile.TemporaryDirectory(prefix="gen3-shared-fixture-") as root:
        barrier = context.Barrier(8)
        results = context.Queue()
        processes = [context.Process(target=worker, args=(root, barrier, results)) for _ in range(8)]
        for process in processes:
            process.start()
        try:
            for process in processes:
                process.join(timeout=15)
                assert process.exitcode == 0, process.exitcode
            assert [results.get(timeout=2) for _ in processes] == [
                {"bucket": "test-bucket", "region": "us-east-1"}
            ] * 8
            events = (Path(root) / "events").read_text().splitlines()
            assert events == [
                "setup main_account", "setup user0_account",
                "cleanup main_account", "cleanup user0_account",
            ] + ["test-start"] * 8, events
        finally:
            for process in processes:
                if process.is_alive():
                    process.terminate()
                process.join()
            results.close()

    with tempfile.TemporaryDirectory(prefix="gen3-failed-fixture-") as root:
        os.environ["FUNNEL_CI_ARTIFACTS_DIR"] = root
        try:
            initialize_storage(Client(root, fail=True), "main_account", "user0_account")
        except AssertionError as error:
            assert "403" in str(error)
        else:
            raise AssertionError("authorization failure was hidden")
        assert not (Path(root) / "workflow-storage.json").exists()
    print("PASS: eight processes initialize shared storage exactly once before tests; authorization failures propagate")


if __name__ == "__main__":
    main()
