#!/usr/bin/env python3
"""Exercise the pinned helper's real polling loop with a simulated clock."""

import ast
import importlib.util
import logging
import sys
import tempfile
from pathlib import Path
from types import SimpleNamespace


script = Path(__file__).with_name("configure-tests.py")
spec = importlib.util.spec_from_file_location("configure_polling", script)
configuration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(configuration)


def method(source):
    tree = ast.parse(source)
    workflow = next(node for node in tree.body if isinstance(node, ast.ClassDef) and node.name == "Gen3Workflow")
    return next(node for node in workflow.body if isinstance(node, ast.FunctionDef) and node.name == "poll_until_task_reaches_expected_state")


def exercise(node, state_at):
    clock = SimpleNamespace(now=0)
    diagnostics = []

    def sleep(seconds):
        clock.now += seconds

    namespace = {
        "time": SimpleNamespace(sleep=sleep),
        "json": SimpleNamespace(dumps=lambda *args, **kwargs: "task"),
        "logger": logging.getLogger("polling-test"),
        "_print_tes_apps_logs": lambda **kwargs: diagnostics.append(clock.now),
    }
    exec(compile(ast.Module(body=[node], type_ignores=[]), "pinned-gen3-poller", "exec"), namespace)
    client = SimpleNamespace(get_tes_task=lambda **kwargs: {"state": state_at(clock.now)})
    error = None
    try:
        namespace[node.name](client, "task", "user", "COMPLETE")
    except Exception as caught:
        error = caught
    return clock.now, diagnostics, error


def main(test_root):
    original = (test_root / "services" / "gen3workflow.py").read_text()
    original_conftest = (test_root / "conftest.py").read_text()
    with tempfile.TemporaryDirectory(prefix="gen3-polling-test-") as directory:
        copy = Path(directory)
        (copy / "services").mkdir()
        destination = copy / "services" / "gen3workflow.py"
        destination.write_text(original)
        conftest = copy / "conftest.py"
        conftest.write_text(original_conftest)
        (copy / "tests").mkdir()
        workflow_test = copy / "tests" / "test_gen3_workflow.py"
        workflow_test.write_text((test_root / "tests" / "test_gen3_workflow.py").read_text())
        original_workflow = workflow_test.read_text()
        configuration.configure(copy)
        assert 'app=funnel-worker,task-id={task_id} --all-containers' in workflow_test.read_text()
        assert workflow_test.read_text().replace(
            'app=funnel-worker,task-id={task_id}', 'app=funnel-worker'
        ).replace(configuration.STORAGE_CONFIGURED, configuration.STORAGE_ORIGINAL) == original_workflow
        configured = destination.read_text()
        configured_conftest = conftest.read_text()
        configuration.configure(copy)
        assert destination.read_text() == configured
        assert conftest.read_text() == configured_conftest

    before, after = method(original), method(configured)
    assert [ast.dump(node) for node in before.body] == [ast.dump(node) for node in after.body]
    assert exercise(before, lambda now: "COMPLETE" if now >= 4 else "RUNNING") == (15, [], None)
    assert exercise(after, lambda now: "COMPLETE" if now >= 4 else "RUNNING") == (4, [], None)
    elapsed, diagnostics, error = exercise(after, lambda now: "RUNNING")
    assert elapsed == 180 and diagnostics == [180] and error is not None
    elapsed, diagnostics, error = exercise(after, lambda now: "SYSTEM_ERROR")
    assert elapsed == 0 and diagnostics == [0] and isinstance(error, AssertionError)
    auth_configure = next(node for node in ast.parse(configured_conftest).body if isinstance(node, ast.FunctionDef) and node.name == "pytest_configure")
    original_auth = next(node for node in ast.parse(original_conftest).body if isinstance(node, ast.FunctionDef) and node.name == "pytest_configure")
    assert ast.dump(auth_configure.body[0].body[1]) == ast.dump(original_auth.body[0].body[1])
    assert [ast.dump(node) for node in auth_configure.body[1:]] == [ast.dump(node) for node in original_auth.body[1:]]
    auth_configure.body = auth_configure.body[:1]
    created = []
    namespace = {
        "os": SimpleNamespace(getenv=lambda key: None),
        "generate_api_keys_for_test_users": lambda: created.append("keys") or "success",
    }
    exec(compile(ast.Module(body=[auth_configure], type_ignores=[]), "pinned-gen3-bootstrap", "exec"), namespace)
    namespace[auth_configure.name](SimpleNamespace())
    for _ in range(4):
        namespace[auth_configure.name](SimpleNamespace(workerinput={}))
    assert created == ["keys"]
    print("PASS: responsive task polling, diagnostics at 180 simulated seconds, unchanged terminal/output assertions, one API-key bootstrap for controller plus four workers")


if __name__ == "__main__":
    main(Path(sys.argv[1]))
