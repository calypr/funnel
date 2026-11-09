#!/usr/bin/env python3
"""Cache full, immutable ARM fixture images as portable OCI archives."""
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile

def interrupted(signum, _frame):
    raise SystemExit(128 + signum)


signal.signal(signal.SIGTERM, interrupted)
fixture = Path(__file__).parent
platform = os.environ["FUNNEL_CI_BUILD_PLATFORM"]
architecture = platform.removeprefix("linux/")
cluster = os.environ["FUNNEL_CI_KIND_CLUSTER_NAME"]
worker = cluster + "-worker"
root = Path(os.environ.get("FUNNEL_CI_FIXTURE_CACHE_DIR", os.environ["FUNNEL_CI_STATE_DIR"] + "/fixture-images"))


def canonical(reference):
    repository, digest = reference.rsplit("@", 1)
    if repository.rfind(":") > repository.rfind("/"):
        repository = repository.rsplit(":", 1)[0]
    return repository + "@" + digest


def yaml(path):
    return json.loads(subprocess.check_output(["yq", "-o=json", str(path)]))


images = {name: item for name, item in json.loads((fixture / "fixture-images.json").read_text())["images"].items()
          if name != "quay.io/cdis/arborist:master"}
services = [item["reference"] for item in images.values()]
bootstrap = [
    item["reference"] for name, item in images.items()
    if "/minio:" in name or "/docker-bitnami-pgvector:" in name
]
services = [reference for reference in services if reference not in bootstrap]
auth = [
    item["reference"] for name, item in images.items()
    if any("/" + service + ":" in name for service in ("arborist", "fence", "indexd"))
]
services = [reference for reference in services if reference not in auth]
csi = yaml(fixture / ("csi-images-arm64.yaml" if architecture == "arm64" else "csi-images.yaml"))
csi_images = [csi["image"], *(value["image"] for value in csi["sidecars"].values())]
manifest = {
    "format": 4,
    "auth": sorted({canonical(reference) for reference in auth}),
    "bootstrap": sorted({canonical(reference) for reference in bootstrap}),
    "platform": platform,
    "services": sorted({canonical(reference) for reference in services}),
    "csi": sorted({canonical(item["repository"] + ":" + item["tag"]) for item in csi_images}),
}
key = hashlib.sha256(json.dumps(manifest, sort_keys=True).encode()).hexdigest()
cache = root / key


def command(*arguments, **kwargs):
    return subprocess.run(["docker", "exec", *arguments], check=True, **kwargs)


def verify(node, references):
    for reference in references:
        result = command(node, "crictl", "inspecti", reference, capture_output=True, text=True)
        image = json.loads(result.stdout)
        actual = image["info"]["imageSpec"]["architecture"]
        if actual != architecture:
            raise RuntimeError(f"Wrong fixture architecture: {reference}: {actual}, expected {architecture}")
    print(f"Verified {len(references)} {architecture} fixture images on {node}", flush=True)


def ready():
    marker = cache / "manifest.json"
    if not marker.exists():
        return False
    if json.loads(marker.read_text()) != manifest:
        raise RuntimeError(f"Fixture cache metadata mismatch: {cache}")
    for name in ("bootstrap", "auth", "services", "csi"):
        if not (cache / (name + ".tar")).is_file():
            raise RuntimeError(f"Incomplete fixture cache: {cache}")
    return True


def save():
    if not ready():
        verify(worker, manifest["bootstrap"] + manifest["auth"] + manifest["services"] + manifest["csi"])
        root.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix="export-", dir=root) as temporary:
            staging = Path(temporary)
            for name in ("bootstrap", "auth", "services", "csi"):
                with (staging / (name + ".tar")).open("wb") as archive:
                    command(worker, "ctr", "-n", "k8s.io", "images", "export", "--local", "--platform", platform,
                            "--skip-manifest-json", "-", *manifest[name], stdout=archive)
            (staging / "manifest.json").write_text(json.dumps(manifest, sort_keys=True))
            if cache.exists():
                shutil.rmtree(cache)
            staging.rename(cache)
    output = os.environ.get("GITHUB_OUTPUT")
    if output:
        with open(output, "a") as stream:
            stream.write("fixture_cache_ready=true\n")
    print(f"Full fixture image cache ready: {cache}", flush=True)


mode = sys.argv[1]
if mode in ("restore-bootstrap", "restore-auth", "restore-services"):
    if not ready():
        print(f"Fixture image cache miss: {key}", flush=True)
    else:
        def restore_archive(node, name):
            with (cache / (name + ".tar")).open("rb") as archive:
                subprocess.run(["docker", "exec", "-i", node, "ctr", "-n", "k8s.io", "images", "import", "--local",
                                "--platform", platform, "-"], stdin=archive, check=True)
            verify(node, manifest[name])

        # CSI runs on both nodes independently of the worker's service images.
        with ThreadPoolExecutor(max_workers=3) as pool:
            if mode == "restore-bootstrap":
                targets = ((worker, "bootstrap"), (worker, "csi"), (cluster + "-control-plane", "csi"))
            else:
                targets = ((worker, mode.removeprefix("restore-")),)
            pending = [pool.submit(restore_archive, node, name) for node, name in targets]
            for result in pending:
                result.result()
        print(f"Restored {mode.removeprefix('restore-')} fixture image cache: {key}", flush=True)
elif mode == "seed":
    if not ready():
        for reference in manifest["bootstrap"] + manifest["auth"] + manifest["services"] + manifest["csi"]:
            present = subprocess.run(["docker", "exec", worker, "crictl", "inspecti", reference],
                                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if present.returncode:
                print(f"One-time fixture fetch: {reference}", flush=True)
                command(worker, "timeout", "180", "crictl", "pull", reference)
            # Older Kind fixtures may have discarded compressed blobs after unpacking.
            command(worker, "timeout", "180", "ctr", "-n", "k8s.io", "content", "fetch", "--platform", platform, reference,
                    stdout=subprocess.DEVNULL)
    save()
elif mode == "save":
    save()
else:
    raise SystemExit("Expected restore-bootstrap, restore-auth, restore-services, save, or seed")
