#!/usr/bin/env python3
"""Fetch the portable source layers of the original DICOM test image.

Original image: quay.io/cdis/gen3-workflow@sha256:f8b8ea43994a865b27a8e1d547406b0c2cc2f2e818eb3230701fc55270f8ac8a
Only requirements.txt and the original Python utilities are extracted; no AMD64
binary is executed or copied into the native image.
"""
import hashlib
import io
import json
from pathlib import Path
import sys
import tarfile
import urllib.request

output = Path(sys.argv[1])
token_url = "https://quay.io/v2/auth?service=quay.io&scope=repository:cdis/gen3-workflow:pull"
with urllib.request.urlopen(token_url, timeout=20) as response:
    token = json.load(response)["token"]
for digest in (
    "6b398299e31b126e642767d3523c4eac5e318ee509aaeaeccac474c95c77f139",
    "845ab5db8b0b37c7647e949e9c539e5aed027cd64ee511bc08f76653fe38e290",
):
    request = urllib.request.Request(
        "https://quay.io/v2/cdis/gen3-workflow/blobs/sha256:" + digest,
        headers={"Authorization": "Bearer " + token},
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        data = response.read()
    if hashlib.sha256(data).hexdigest() != digest:
        raise ValueError("DICOM source layer checksum mismatch")
    with tarfile.open(fileobj=io.BytesIO(data)) as archive:
        archive.extractall(output, filter="data")
print("Verified original DICOM requirements and Python utilities")
