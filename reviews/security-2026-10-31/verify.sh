#!/usr/bin/env bash
set -euo pipefail
review_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(git -C "$review_dir" rev-parse --show-toplevel)
review_commit=6c233af6e06e0a404dbc353310dab2c886de0b45
audit_dir=$(mktemp -d /tmp/funnel-security-review.XXXXXX)
mkdir -p "$audit_dir/source" "$audit_dir/evidence" "$audit_dir/probes"
git -C "$repo_dir" archive "$review_commit" | tar -x -C "$audit_dir/source"
export GOCACHE="${GOCACHE:-/tmp/funnel-security-gocache}"
export GOPROXY=off
cp "$review_dir/probes/oidc_audit_test.go.txt" "$audit_dir/source/server/security_audit_test.go"
cp "$review_dir/probes/gateway_audit_test.go.txt" "$audit_dir/source/server/security_gateway_test.go"
cp "$review_dir/probes/rpc_audit_test.go.txt" "$audit_dir/source/util/rpc/security_audit_test.go"
cp "$review_dir/probes/plugin_audit_test.go.txt" "$audit_dir/source/compute/local/security_audit_test.go"
cp "$review_dir/probes/gcp_audit_test.go.txt" "$audit_dir/source/compute/gcp_batch/security_audit_test.go"
cp "$review_dir/probes/api_audit_test.go.txt" "$audit_dir/probes/api_audit_test.go"
for probe in docker yaml mapper logger config_safe ftp local_containment credential_transport; do
  cp "$review_dir/probes/${probe}_probe.go.txt" "$audit_dir/probes/${probe}.go"
done
# Mapper fixtures stay inside this run's temporary directory.
python3 - "$audit_dir/probes" <<'PY'
from pathlib import Path
import sys
probes = Path(sys.argv[1])
for name in ('docker', 'mapper'):
    p = probes / (name + '.go')
    p.write_text(p.read_text().replace('/tmp/funnel-audit-storage', str(probes / 'mapper-fixture')))
PY
cd "$audit_dir/source"
go test ./server ./util/rpc ./compute/local ./compute/gcp_batch -run '^TestAudit' -v -count=1 > "$audit_dir/evidence/unit-checks.txt" 2>&1
go test -v -count=1 "$audit_dir/probes/api_audit_test.go" > "$audit_dir/evidence/api-checks.txt" 2>&1
for probe in docker yaml mapper config_safe local_containment credential_transport; do
  go run "$audit_dir/probes/${probe}.go" > "$audit_dir/evidence/${probe}.txt" 2>&1
done
go run "$audit_dir/probes/logger.go" > "$audit_dir/evidence/logger.stdout" 2> "$audit_dir/evidence/logger.stderr"
python3 "$review_dir/probes/redaction_probe.py" > "$audit_dir/evidence/redaction.txt"
if [[ "${1:-}" == --with-loopback ]]; then
  go run "$audit_dir/probes/ftp.go" > "$audit_dir/evidence/ftp.txt" 2>&1
else
  printf '%s\n' 'FTP probe skipped: rerun with --with-loopback to permit a synthetic 127.0.0.1 listener.' > "$audit_dir/evidence/ftp.txt"
fi
python3 - "$audit_dir/evidence" <<'PYVERIFY'
from pathlib import Path
import sys
root = Path(sys.argv[1])
checks = {
    'unit-checks.txt': ['configured OIDC initialization panicked:', 'fresh session cookies=0 callback status=307 sets-session-cookie=true', 'wrong audience accepted=true', 'missing expiry accepted=true', 'expired control accepted=false', 'wrong audience with explicit policy accepted=false', 'fully_decoded_description_bytes=5242880 response_status=401', 'credentials_received=true transport_auth=<nil>', 'shared configuration user after per-request plugin response=alice', 'serialized_container_options="--workdir /work --privileged"', 'cancel with no cloud client returned=<nil>'],
    'api-checks.txt': ['metadata=map[audit:bob-via-grpc]', 'bob delete node=<nil>', 'name=bob-overwrite state=COMPLETE; alice read after=permission denied', 'bob claim=<nil> error=permission denied', 'alice claim=claimed:true error=<nil>'],
    'docker.txt': ['"--privileged"', 'error=<nil>'],
    'yaml.txt': ['privileged=true allowPrivilegeEscalation=true'],
    'mapper.txt': ['sibling_contents="synthetic attacker write" error=<nil>'],
    'config_safe.txt': ['mongo_uri_password_retained=true', 'separate_password_redacted=true'],
    'local_containment.txt': ['case=allowed-sibling/marker allowed=true', 'case=allowed/../allowed-sibling/marker allowed=true', 'case=allowed/link/marker allowed=true'],
    'credential_transport.txt': ['scheme=http credentials=true'],
    'logger.stdout': ['application_debug_suppressed=true'],
    'logger.stderr': ['synthetic-secret'],
    'redaction.txt': ['named: changed=True', 'presign: changed=False', 'envyaml: changed=False', 'userinfo: changed=False'],
}
for name, markers in checks.items():
    text = (root / name).read_text()
    for marker in markers:
        if marker not in text:
            raise SystemExit(f'Recorded behavior was not reproduced: {name}: {marker}')
print('All recorded local behavior checks reproduced.')
PYVERIFY
printf 'Reviewed commit: %s\nSource: %s\nProbes: %s\nEvidence: %s\n' "$review_commit" "$audit_dir/source" "$audit_dir/probes" "$audit_dir/evidence"
printf '%s\n' 'Passing audit checks reproduce the recorded defects; they do not indicate that the code is secure.'
