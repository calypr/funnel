#!/usr/bin/env python3
"""Verify worker logs wait for startup without hiding failures or secret bytes."""

import subprocess
import unittest
from unittest.mock import patch

import funnel_ci_fixture as fixture


class WorkerLogsTest(unittest.TestCase):
    def test_container_creating_then_logs(self):
        creating = subprocess.CompletedProcess([], 1, b"", b"container is waiting to start: ContainerCreating")
        ready = subprocess.CompletedProcess([], 0, b"worker logs with secret-marker", b"")
        with patch.object(fixture.subprocess, "run", side_effect=[creating, ready]) as run, \
                patch.object(fixture.time, "sleep") as sleep:
            result = fixture.worker_logs("jobs", "task-123")
        self.assertIs(result, ready)
        self.assertIn(b"secret-marker", result.stdout)
        self.assertEqual(run.call_count, 2)
        self.assertEqual(sleep.call_count, 1)
        self.assertIn("app=funnel-worker,task-id=task-123", run.call_args.args[0])

    def test_other_failures_are_not_retried(self):
        denied = subprocess.CompletedProcess([], 1, b"", b"Forbidden: cannot read pod logs")
        with patch.object(fixture.subprocess, "run", return_value=denied) as run:
            self.assertIs(fixture.worker_logs("jobs", "task-123"), denied)
        self.assertEqual(run.call_count, 1)

    def test_startup_timeout_preserves_failure(self):
        creating = subprocess.CompletedProcess([], 1, b"", b"container is waiting to start: ContainerCreating")
        with patch.object(fixture.subprocess, "run", return_value=creating) as run, \
                patch.object(fixture.time, "monotonic", side_effect=[0, 60]):
            self.assertIs(fixture.worker_logs("jobs", "task-123"), creating)
        self.assertEqual(run.call_count, 1)


if __name__ == "__main__":
    unittest.main()
