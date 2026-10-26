#!/usr/bin/env python3
"""Resolve the reviewed fixture tags to immutable multi-platform images."""
import concurrent.futures
import json
import pathlib
import re
import subprocess

path = pathlib.Path(__file__).with_name("fixture-images.json")
images = json.loads(path.read_text())["images"]


def inspect(reference, *arguments):
    return json.loads(subprocess.check_output(["docker", "buildx", "imagetools", "inspect", *arguments, reference], text=True))


def resolve(reference):
    descriptor = inspect(reference, "--format", "{{json .Manifest}}")
    digest = descriptor["digest"]
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise ValueError(f"Invalid registry digest: {reference}")
    pinned = f"{reference}@{digest}"
    manifests = descriptor.get("manifests", [])
    platforms = {}
    if not manifests:
        manifest = inspect(pinned, "--raw")
        metadata = inspect(pinned, "--format", "{{json .Image}}")
        architecture = metadata["architecture"]
        if metadata["os"] != "linux":
            raise ValueError(f"Not a Linux fixture image: {reference}")
        platforms[architecture] = {"manifest": digest, "config": manifest["config"]["digest"]}
    for manifest in manifests:
        platform = manifest.get("platform", {})
        architecture = platform.get("architecture")
        if platform.get("os") != "linux" or architecture not in ("amd64", "arm64"):
            continue
        child = inspect(reference.split("@")[0] + "@" + manifest["digest"], "--raw")
        platforms[architecture] = {"manifest": manifest["digest"], "config": child["config"]["digest"]}
    if "arm64" not in platforms:
        raise ValueError(f"No Linux ARM64 fixture image: {reference}")
    return reference, {"reference": pinned, "platforms": platforms}


with concurrent.futures.ThreadPoolExecutor(max_workers=7) as executor:
    locked = dict(executor.map(resolve, images))
path.write_text(json.dumps({"images": locked}, indent=2, sort_keys=True) + "\n")
print(f"Locked {len(locked)} fixture images in {path}")
