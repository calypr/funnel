"""Coordinate shared storage and worker log availability for Gen3 tests."""

import fcntl
import json
import os
import subprocess
import time
from pathlib import Path


def worker_logs(namespace: str, task_id: str) -> subprocess.CompletedProcess[bytes]:
    deadline = time.monotonic() + 60
    while True:
        result = subprocess.run(
            [
                "kubectl", "-n", namespace, "logs", "-l",
                f"app=funnel-worker,task-id={task_id}",
                "--all-containers", "--tail", "-1",
            ],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=15,
        )
        starting = any(
            b"is waiting to start: " + reason in result.stderr
            for reason in (b"ContainerCreating", b"PodInitializing")
        )
        if result.returncode == 0 or not starting or time.monotonic() >= deadline:
            return result
        time.sleep(1)


def initialize_storage(client, primary_user, secondary_user):
    root = Path(os.environ["FUNNEL_CI_ARTIFACTS_DIR"])
    root.mkdir(parents=True, exist_ok=True)
    state = root / "workflow-storage.json"
    with (root / "workflow-storage.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if state.exists():
            return json.loads(state.read_text())

        storage = client.setup_storage(user=primary_user, expected_status=200)
        client.setup_storage(user=secondary_user, expected_status=200)
        client.cleanup_user_bucket(user=primary_user)
        client.cleanup_user_bucket(user=secondary_user)
        temporary = state.with_suffix(".tmp")
        temporary.write_text(json.dumps(storage))
        temporary.replace(state)
        return storage
