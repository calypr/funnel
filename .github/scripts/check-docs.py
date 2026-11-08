#!/usr/bin/env python3
"""Check that documentation stays readable without a website renderer."""

import re
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[2]
DOCS = ROOT / "docs"


def source_errors():
    for path in sorted(DOCS.rglob("*.md")):
        if "internal" in path.relative_to(DOCS).parts:
            continue
        text = path.read_text()
        name = path.relative_to(ROOT)
        if not text.startswith("# "):
            yield f"{name}: start with a Markdown heading, not website metadata"
        if re.search(r"\{\{[<%]", text):
            yield f"{name}: replace Hugo shortcodes with ordinary Markdown"
        fence = None
        for number, line in enumerate(text.splitlines(), 1):
            marker = re.match(r"\s*(`{3,}|~{3,})", line)
            if marker:
                if fence is None:
                    fence = marker[1][0]
                elif marker[1][0] == fence:
                    fence = None
                continue
            if fence:
                continue
            destinations = re.findall(r"\]\(([^\s)]*)\)", line)
            destinations += re.findall(r"^\[[^\]]+\]:\s*(\S+)", line)
            for destination in destinations:
                url = urlsplit(destination)
                if url.scheme or url.netloc or destination.startswith("#"):
                    continue
                target = path.parent / unquote(url.path)
                if not destination or url.path.startswith("/") or not target.is_file():
                    yield f"{name}:{number}: use a relative link to an existing source file: {destination!r}"


def main():
    errors = list(source_errors())
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print("Documentation Markdown checks passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
