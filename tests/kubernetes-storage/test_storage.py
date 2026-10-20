#!/usr/bin/env python3
"""Real TES tasks, Kubernetes PVCs, and S3 transfers. Run through run.sh."""

import contextlib
import json
import os
from pathlib import Path
import re
import subprocess
import time
import unittest
import urllib.error
import urllib.request

NAMESPACE = "funnel-storage-e2e"
OUTPUT = Path(os.environ["FUNNEL_E2E_OUTPUT_DIR"])
IMAGE = os.environ["FUNNEL_E2E_IMAGE"]
TERMINAL = {"COMPLETE", "EXECUTOR_ERROR", "SYSTEM_ERROR", "CANCELED"}


def kubectl(*args, stdin=None):
    result = subprocess.run(
        ["kubectl", "-n", NAMESPACE, *args], input=stdin,
        text=True, capture_output=True, timeout=150, check=False,
    )
    if result.returncode:
        raise RuntimeError(f"kubectl {' '.join(args)}: {result.stderr}")
    return result.stdout


def apply(kind, name, **fields):
    api_version = "v1"
    if kind in {"Role", "RoleBinding"}:
        api_version = "rbac.authorization.k8s.io/v1"
    manifest = {
        "apiVersion": api_version, "kind": kind,
        "metadata": {"name": name, "namespace": NAMESPACE}, **fields,
    }
    kubectl("apply", "-f", "-", stdin=json.dumps(manifest))


def get(kind, name):
    raw = kubectl("get", kind, name, "--ignore-not-found", "-o", "json")
    return json.loads(raw) if raw.strip() else None


def wait_for(description, predicate, timeout=150):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = predicate()
        if result:
            return result
        time.sleep(0.5)
    raise AssertionError(f"Timed out waiting for {description}")


def pod(name, claim, mount_path):
    apply("Pod", name, spec={
        "restartPolicy": "Never", "terminationGracePeriodSeconds": 1,
        "containers": [{
            "name": "probe", "image": "alpine:3.21", "command": ["sleep", "900"],
            "volumeMounts": [{"name": "data", "mountPath": mount_path}],
        }],
        "volumes": [{"name": "data", "persistentVolumeClaim": {"claimName": claim}}],
    })
    kubectl("wait", "--for=condition=Ready", "pod/" + name, "--timeout=120s")


def pvc(name, mode="ReadWriteOnce"):
    # Deliberately omit storageClassName, as in Argo's volume-claim fixture.
    apply("PersistentVolumeClaim", name, spec={
        "accessModes": [mode], "resources": {"requests": {"storage": "64Mi"}},
    })


def request(url, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url + path, data=data, headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=15) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        raise AssertionError(f"TES {path}: {error.code}: {error.read().decode()}") from error


def finished(url, task_id, expected):
    def check():
        task = request(url, f"/v1/tasks/{task_id}?view=FULL")
        if task["state"] not in TERMINAL:
            return False
        (OUTPUT / f"task-{task_id}.json").write_text(json.dumps(task, indent=2))
        if task["state"] != expected:
            raise AssertionError(f"Expected {expected}, received {json.dumps(task)}")
        return task
    return wait_for(f"task {task_id} to reach {expected}", check)


@contextlib.contextmanager
def server(mode):
    name = "funnel-" + mode
    template = Path(__file__).with_name("funnel.yaml").read_text()
    workspace = """    volumeClaimTemplates:
      - metadata:
          name: workspace
        spec:
          accessModes: [ReadWriteOnce]
          resources:
            requests:
              storage: 64Mi
    volumes:"""
    if mode == "existing":
        workspace = """    volumes:
      - name: workspace
        persistentVolumeClaim:
          claimName: shared-workspace"""
    if mode == "readonly-template":
        workspace = workspace.replace("    volumes:", """      - metadata:
          name: output
        spec:
          accessModes: [ReadOnlyMany]
          resources:
            requests:
              storage: 64Mi
    volumes:""")
    readonly = mode.startswith("readonly-")
    replacements = {
        "__SERVER__": name, "__IMAGE__": IMAGE, "__WORKSPACE__": workspace,
        "__MOUNTED_WRITE__": "    MountedWrite:\n      Volume: output" if readonly else "",
        "__OUTPUT_MOUNT__": "      - name: output\n        mountPath: /mounted-output" if readonly else "",
        "__OUTPUT_VOLUME__": (
            "      - name: output\n        persistentVolumeClaim:\n          claimName: readonly-output"
            if mode == "readonly-existing" else ""
        ),
    }
    for key, value in replacements.items():
        template = template.replace(key, value)
    apply("ConfigMap", name, data={
        "server.yaml": template.replace("__DATABASE__", "boltdb"),
        "worker.yaml": template.replace("__DATABASE__", "rpc"),
    })
    apply("Service", name, spec={
        "selector": {"server": name},
        "ports": [{"name": "http", "port": 8000}, {"name": "rpc", "port": 9090}],
    })
    manifest = {
        "apiVersion": "v1", "kind": "Pod",
        "metadata": {"name": name, "namespace": NAMESPACE, "labels": {"server": name}},
        "spec": {
            "serviceAccountName": "funnel", "restartPolicy": "Never", "terminationGracePeriodSeconds": 1,
            "containers": [{
                "name": "server", "image": IMAGE, "imagePullPolicy": "IfNotPresent",
                "args": ["server", "run", "--config", "/config/server.yaml"],
                "volumeMounts": [
                    {"name": "config", "mountPath": "/config", "readOnly": True},
                    {"name": "database", "mountPath": "/database"},
                ],
            }],
            "volumes": [{"name": "config", "configMap": {"name": name}}, {"name": "database", "emptyDir": {}}],
        },
    }
    kubectl("apply", "-f", "-", stdin=json.dumps(manifest))
    forward = None
    try:
        kubectl("wait", "--for=condition=Ready", "pod/" + name, "--timeout=120s")
        log_path = OUTPUT / f"{name}-forward.log"
        with log_path.open("w") as log:
            forward = subprocess.Popen(
                ["kubectl", "-n", NAMESPACE, "port-forward", "pod/" + name, "0:8000"],
                stdout=log, stderr=subprocess.STDOUT,
            )
            match = wait_for("TES port-forward", lambda: re.search(r"127.0.0.1:(\d+) ->", log_path.read_text()))
            url = "http://127.0.0.1:" + match.group(1)
            request(url, "/v1/tasks")
            yield url
    finally:
        if forward is not None:
            forward.terminate()
            forward.wait(timeout=10)
        (OUTPUT / f"{name}.log").write_text(kubectl("logs", name))
        kubectl("delete", "pod", name, "--wait=true", "--timeout=60s")


class StorageE2E(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        apply("ServiceAccount", "funnel")
        apply("Role", "funnel", rules=[
            {"apiGroups": [""], "resources": ["pods", "pods/log", "events", "persistentvolumeclaims"],
             "verbs": ["get", "list", "watch", "create", "update", "patch", "delete"]},
            {"apiGroups": ["batch"], "resources": ["jobs"],
             "verbs": ["get", "list", "watch", "create", "update", "patch", "delete"]},
        ])
        apply("RoleBinding", "funnel", roleRef={"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "funnel"},
              subjects=[{"kind": "ServiceAccount", "name": "funnel", "namespace": NAMESPACE}])
        pvc("references")
        pvc("shared-workspace")
        pvc("readonly-output", "ReadOnlyMany")
        pod("references", "references", "/data")
        kubectl("exec", "references", "--", "sh", "-ec",
                "mkdir -p /data/release-1 /data/release-2; printf 'mounted\\n' > /data/release-1/input.txt; printf 'WRONG\\n' > /data/release-2/input.txt")
        pods = json.loads(kubectl("get", "pods", "-l", "app=minio", "-o", "json"))
        cls.minio = pods["items"][0]["metadata"]["name"]
        kubectl("exec", cls.minio, "--", "sh", "-ec",
                "mc alias set e2e http://127.0.0.1:9000 example example-password >/dev/null; mc mb e2e/local; printf 'api\\n' | mc pipe e2e/local/api/input.txt")
        # The reference exists only on the PVC: an accidental API fallback must fail.
        cls.assert_no_reference_object()

    @classmethod
    def assert_no_reference_object(cls):
        objects = kubectl("exec", cls.minio, "--", "mc", "ls", "--recursive", "e2e/local/references/")
        if objects.strip():
            raise AssertionError("Mounted-read fixture unexpectedly exists in S3")

    def test_hello_world(self):
        with server("hello-world") as url:
            task_id = request(url, "/v1/tasks", {
                "name": "hello world",
                "executors": [{
                    "image": "alpine:3.21",
                    "command": ["sh", "-ec", "echo hello world"],
                }],
            })["id"]
            task = finished(url, task_id, "COMPLETE")
            stdout = [
                executor_log.get("stdout", "")
                for task_log in task.get("logs", [])
                for executor_log in task_log.get("logs", [])
            ]
            self.assertEqual(stdout, ["hello world\n"], task)

    def task(self, prefix, gate=True):
        command = """set -eu
cat /inputs/api.txt /inputs/reference.txt > /ephemeral/combined
mkdir -p /outputs/nested
cp /ephemeral/combined /outputs/result.txt
printf 'nested\n' > /outputs/nested/value.txt
: > /outputs/empty.txt
"""
        if gate:
            command += "touch /scratch/started\nwhile [ ! -f /scratch/release ]; do sleep 0.2; done\n"
        return {
            "name": prefix,
            "inputs": [
                {"url": "s3://local/api/input.txt", "path": "/inputs/api.txt", "type": "FILE"},
                {"url": "s3://local/references/input.txt", "path": "/inputs/reference.txt", "type": "FILE"},
            ],
            "outputs": [{"url": f"s3://local/{prefix}/", "path": "/outputs", "type": "DIRECTORY"}],
            "volumes": ["/scratch"],
            "executors": [{"image": "alpine:3.21", "command": ["sh", "-ec", command]}],
        }

    def test_volume_transfers_and_cleanup(self):
        for mode in ("dynamic", "existing"):
            with self.subTest(mode=mode), server(mode) as url:
                task_id = request(url, "/v1/tasks", self.task(mode))["id"]
                claim_name = "shared-workspace" if mode == "existing" else task_id + "-workspace"
                claim = wait_for("workspace PVC", lambda: get("pvc", claim_name))
                self.assertEqual(claim["spec"]["storageClassName"], "local-path")
                probe = "probe-" + mode
                pod(probe, claim_name, "/workspace")
                try:
                    wait_for("executor to stage inputs", lambda: kubectl(
                        "exec", probe, "--", "sh", "-c", f"test ! -f /workspace/{task_id}/scratch/started || echo ready"
                    ).strip())
                    worker = get("job", task_id)
                    self.assertIsNotNone(worker)
                    plan = json.loads(worker["metadata"]["annotations"]["funnel.calypr.org/storage-plan"])
                    self.assertEqual(plan["OwnedClaims"] or [], [] if mode == "existing" else [claim_name])
                    self.assertEqual(plan["S3ReadMounts"][0]["Root"], "/reference")
                    jobs = json.loads(kubectl("get", "jobs", "-l", "app=funnel-executor", "-o", "json"))["items"]
                    executor = next(job for job in jobs if job["metadata"]["name"].startswith(task_id))
                    volumes = executor["spec"]["template"]["spec"]["volumes"]
                    self.assertTrue(any(volume.get("emptyDir") == {} for volume in volumes))
                    self.assertFalse(any(volume.get("persistentVolumeClaim", {}).get("claimName") == "references" for volume in volumes))
                    self.assertEqual(request(url, f"/v1/tasks/{task_id}")["state"], "RUNNING")
                    kubectl("exec", probe, "--", "touch", f"/workspace/{task_id}/scratch/release")
                    task = finished(url, task_id, "COMPLETE")
                    expected = {"result.txt": "api\nmounted\n", "nested/value.txt": "nested\n", "empty.txt": ""}
                    logs = [output for log in task.get("logs", []) for output in log.get("outputs", [])]
                    self.assertEqual(len(logs), len(expected), logs)
                    self.assertEqual({item["url"]: int(item.get("size_bytes", 0)) for item in logs},
                                     {f"s3://local/{mode}/{key}": len(value) for key, value in expected.items()})
                    for key, content in expected.items():
                        self.assertEqual(kubectl("exec", self.minio, "--", "mc", "cat", f"e2e/local/{mode}/{key}"), content)
                    wait_for("workspace directory cleanup", lambda: kubectl(
                        "exec", probe, "--", "sh", "-c", f'if test ! -d /workspace/{task_id} || test -z "$(find /workspace/{task_id} -mindepth 1 -print -quit)"; then echo empty; fi'
                    ).strip())
                finally:
                    kubectl("delete", "pod", probe, "--wait=true", "--timeout=60s")
                wait_for("worker Job cleanup", lambda: get("job", task_id) is None)
                if mode == "dynamic":
                    wait_for("owned PVC cleanup", lambda: get("pvc", claim_name) is None)
                else:
                    self.assertFalse(get("pvc", claim_name)["metadata"].get("ownerReferences"))
                self.assertEqual(kubectl("exec", "references", "--", "cat", "/data/release-1/input.txt"), "mounted\n")
                self.assertIsNotNone(get("pvc", "references"))

    def test_cancel_cleans_owned_workspace(self):
        with server("cancel") as url:
            task_id = request(url, "/v1/tasks", self.task("cancel"))["id"]
            wait_for("running task", lambda: request(url, f"/v1/tasks/{task_id}")["state"] == "RUNNING")
            self.assertIsNotNone(get("pvc", task_id + "-workspace"))
            request(url, f"/v1/tasks/{task_id}:cancel", {})
            finished(url, task_id, "CANCELED")
            wait_for("canceled worker cleanup", lambda: get("job", task_id) is None)
            wait_for("canceled PVC cleanup", lambda: get("pvc", task_id + "-workspace") is None)
            self.assertIsNotNone(get("pvc", "references"))

    def test_upload_failure_is_not_success(self):
        kubectl("exec", self.minio, "--", "mc", "mb", "e2e/failure-bucket")
        with server("upload-failure") as url:
            task = self.task("upload-failure")
            task["outputs"][0]["url"] = "s3://failure-bucket/output/"
            task_id = request(url, "/v1/tasks", task)["id"]
            claim_name = task_id + "-workspace"
            wait_for("workspace PVC", lambda: get("pvc", claim_name))
            pod("probe-failure", claim_name, "/workspace")
            try:
                wait_for("executor output", lambda: kubectl(
                    "exec", "probe-failure", "--", "sh", "-c",
                    f"test ! -f /workspace/{task_id}/scratch/started || echo ready"
                ).strip())
                kubectl("exec", self.minio, "--", "mc", "rb", "e2e/failure-bucket")
                kubectl("exec", "probe-failure", "--", "touch", f"/workspace/{task_id}/scratch/release")
                result = finished(url, task_id, "SYSTEM_ERROR")
                self.assertFalse([output for log in result.get("logs", []) for output in log.get("outputs", [])])
                execution_logs = [executor for log in result.get("logs", []) for executor in log.get("logs", [])]
                self.assertTrue(execution_logs, result)
                self.assertTrue(all(int(log.get("exit_code", 0)) == 0 for log in execution_logs), execution_logs)
                self.assertIn("failure-bucket", json.dumps(result.get("logs", [])))
            finally:
                kubectl("delete", "pod", "probe-failure", "--wait=true", "--timeout=60s")
            wait_for("failed task Job cleanup", lambda: get("job", task_id) is None)
            wait_for("failed task PVC cleanup", lambda: get("pvc", claim_name) is None)

    def test_readonly_outputs_rejected_before_worker_creation(self):
        for mode in ("readonly-existing", "readonly-template"):
            with self.subTest(mode=mode), server(mode) as url:
                task_id = request(url, "/v1/tasks", self.task(mode, gate=False))["id"]
                task = finished(url, task_id, "SYSTEM_ERROR")
                self.assertIn("writable access mode", json.dumps(task))
                self.assertIsNone(get("job", task_id))
                self.assertIsNone(get("pvc", task_id + "-workspace"))
                self.assertIsNone(get("pvc", task_id + "-output"))
                self.assertIsNotNone(get("pvc", "readonly-output"))


if __name__ == "__main__":
    unittest.main(verbosity=2)
