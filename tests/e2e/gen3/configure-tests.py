#!/usr/bin/env python3
"""Configure the pinned Gen3 suite for the disposable CI environment."""

import sys
from pathlib import Path


ORIGINAL = "        max_retries=30,\n        poll_interval=15,\n"
CONFIGURED = "        max_retries=90,\n        poll_interval=2,\n"
AUTH_ORIGINAL = '''    if not os.getenv("RUNNING_LOCAL"):
        result = generate_api_keys_for_test_users()
'''
AUTH_CONFIGURED = '''    if not os.getenv("RUNNING_LOCAL") and not hasattr(config, "workerinput"):
        result = generate_api_keys_for_test_users()
'''

STORAGE_ORIGINAL = """        # Hit the storage setup endpoint for all users who will be creating tasks.
        cls.s3_storage_config = WorkflowStorageConfig.from_dict(
            cls.gen3_workflow.setup_storage(user=cls.valid_user, expected_status=200)
        )
        cls.gen3_workflow.setup_storage(user=cls.other_valid_user, expected_status=200)

        # Ensure the bucket is emptied before running the tests (must run after
        # `storage_setup` so the user has access to empty the bucket)
        cls.gen3_workflow.cleanup_user_bucket()
        cls.gen3_workflow.cleanup_user_bucket(user=cls.other_valid_user)
"""
STORAGE_CONFIGURED = """        from funnel_ci_fixture import initialize_storage

        cls.s3_storage_config = WorkflowStorageConfig.from_dict(
            initialize_storage(cls.gen3_workflow, cls.valid_user, cls.other_valid_user)
        )
"""


def replace_once(path: Path, original: str, configured: str) -> None:
    source = path.read_text()
    if source.count(configured) == 1 and original not in source:
        return
    if source.count(original) != 1:
        raise SystemExit(f"The pinned Gen3 configuration changed in {path.name}")
    path.write_text(source.replace(original, configured, 1))


def configure(test_root: Path) -> None:
    replace_once(test_root / "services" / "gen3workflow.py", ORIGINAL, CONFIGURED)
    replace_once(test_root / "conftest.py", AUTH_ORIGINAL, AUTH_CONFIGURED)
    replace_once(
        test_root / "tests" / "test_gen3_workflow.py",
        'logs -l app=funnel-worker --all-containers --tail -1',
        'logs -l app=funnel-worker,task-id={task_id} --all-containers --tail -1',
    )
    replace_once(test_root / "tests" / "test_gen3_workflow.py", STORAGE_ORIGINAL, STORAGE_CONFIGURED)
    (test_root / "funnel_ci_fixture.py").write_text(
        Path(__file__).with_name("funnel_ci_fixture.py").read_text()
    )
    print("Gen3 shared storage initialized once per suite; TES polling: 90 attempts at 2-second intervals; API keys created once by the pytest controller")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit(f"usage: {sys.argv[0]} <gen3-test-root>")
    configure(Path(sys.argv[1]))
