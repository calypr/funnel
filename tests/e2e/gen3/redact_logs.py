#!/usr/bin/env python3
"""Redact credentials from line-oriented test and Kubernetes logs."""

from __future__ import annotations

import re
import sys

BEARER_TOKEN = re.compile(r"(?i)(Bearer\s+)[^\s,;]+")
JWT = re.compile(r"eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}")
AUTHORIZATION_HEADER = re.compile(r"(?i)(['\"]?authorization['\"]?\s*[:=]\s*)[^\r\n]+")
SECRET_KEYS = (
    r"(?:access[_-]?token|refresh[_-]?token|id[_-]?token|session[_-]?token|token|"
    r"api[_-]?key|aws[_-]?access[_-]?key(?:[_-]?id)?|access[_-]?key(?:[_-]?id)?|"
    r"aws[_-]?secret[_-]?access[_-]?key|secret[_-]?(?:access[_-]?)?key|"
    r"client[_-]?secret|secret|password)"
)
SECRET_VALUE = re.compile(
    rf"(?i)(['\"]?{SECRET_KEYS}['\"]?\s*[:=]\s*['\"]?)[^\s,&'\"]+"
)


def redact(text: str) -> str:
    """Return log text with bearer credentials, JWTs, and named secrets masked."""
    text = AUTHORIZATION_HEADER.sub(r"\1[REDACTED]", text)
    text = BEARER_TOKEN.sub(r"\1[REDACTED]", text)
    text = JWT.sub("[REDACTED JWT]", text)
    return SECRET_VALUE.sub(lambda match: match.group(1) + "[REDACTED]", text)


def main() -> None:
    for line in sys.stdin:
        sys.stdout.write(redact(line))
        sys.stdout.flush()


if __name__ == "__main__":
    main()
