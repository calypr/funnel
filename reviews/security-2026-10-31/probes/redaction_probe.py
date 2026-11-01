from pathlib import Path

namespace = {"__name__": "audit"}
exec(compile(Path("tests/gen3/redact_logs.py").read_text(), "redact_logs.py", "exec"), namespace)
fixtures = {
    "named": "password=synthetic-secret",
    "presign": "https://example.invalid/object?X-Amz-Credential=synthetic-id&X-Amz-Signature=synthetic-signature",
    "envyaml": "- name: PASSWORD\n  value: synthetic-secret",
    "userinfo": "ftp://user:synthetic-secret@example.invalid/file",
}
for label, fixture in fixtures.items():
    result = namespace["redact"](fixture)
    print(f"{label}: changed={result != fixture}; output={result!r}")
