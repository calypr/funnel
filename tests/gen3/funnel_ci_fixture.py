"""Initialize shared Gen3 test storage once before any worker starts using it."""

import fcntl
import json
import os
from pathlib import Path


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
