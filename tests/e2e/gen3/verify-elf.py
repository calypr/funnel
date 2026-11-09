#!/usr/bin/env python3
"""Reject foreign executables even when binfmt would silently translate them."""
import struct
import sys

architecture, executable = sys.argv[1:]
header = sys.stdin.buffer.read(20)
if len(header) != 20 or header[:4] != b"\x7fELF" or header[4:6] != b"\x02\x01":
    raise SystemExit(f"Expected a 64-bit little-endian ELF executable: {executable}")
actual = struct.unpack_from("<H", header, 18)[0]
expected = {"arm64": 183, "amd64": 62}[architecture]
if actual != expected:
    raise SystemExit(f"Foreign executable: {executable}: ELF machine={actual}, expected {expected} ({architecture})")
print(f"Verified native {architecture} executable: {executable}")
