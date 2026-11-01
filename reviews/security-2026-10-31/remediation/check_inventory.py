import re
from collections import Counter
from pathlib import Path

expected = {f"S{i:02}" for i in range(1, 20)} | {f"P{i:02}" for i in range(1, 13)} | {"D01"}
rows = {}
for line in (Path(__file__).resolve().parent.parent / "REMEDIATION.md").read_text().splitlines():
    match = re.match(r"\| ([SPD]\d{2}),[^|]+\| ([^|]+)\|", line)
    if not match:
        continue
    finding, status = match.group(1), match.group(2).strip()
    if finding in rows:
        raise SystemExit(f"Duplicate finding {finding}")
    if not status.startswith(("Fixed", "Deferred", "Partially fixed")):
        raise SystemExit(f"Unfinished finding {finding}: {status}")
    rows[finding] = status.split()[0]
if set(rows) != expected:
    raise SystemExit(f"Inventory mismatch: missing {sorted(expected - rows.keys())}, extra {sorted(rows.keys() - expected)}")
print(f"All {len(rows)} findings have dispositions: {dict(Counter(rows.values()))}")
