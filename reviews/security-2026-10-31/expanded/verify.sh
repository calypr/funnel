#!/usr/bin/env bash
set -euo pipefail
review_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(git -C "$review_dir" rev-parse --show-toplevel)
review_commit=6c233af6e06e0a404dbc353310dab2c886de0b45
review_run=$(mktemp -d /tmp/funnel-expanded-review.XXXXXX)
mkdir -p "$review_run/source" "$review_run/evidence"
git -C "$repo_dir" archive "$review_commit" | tar -x -C "$review_run/source"
cp "$review_dir/checks/permission_test.go.txt" "$review_run/source/util/fsutil/permission_review_test.go"
cp "$review_dir/checks/google_context_test.go.txt" "$review_run/source/storage/google_context_review_test.go"
cp "$review_dir/checks/kafka_config_test.go.txt" "$review_run/source/events/kafka_config_review_test.go"
export GOCACHE="${GOCACHE:-/tmp/funnel-security-gocache}"
export GOPROXY=off
cd "$review_run/source"
go test ./util/fsutil ./storage ./events -run '^TestReview(DirectoryPreservesCallerPermissions|GoogleUploadContext|KafkaConnectionDefaults)$' -v -count=1 -timeout=20s > "$review_run/evidence/checks.txt" 2>&1
python3 - "$review_run/evidence/checks.txt" <<'PY'
from pathlib import Path
import sys
text = Path(sys.argv[1]).read_text()
markers = [
 'caller_umask_before=0077 caller_umask_after=0000 subsequent_file_mode=0666',
 'outbound_calls=1 parent_canceled=true outbound_request_canceled=false returned_error=true',
 'kafka_default_tls=false kafka_default_sasl=false',
]
for marker in markers:
 if marker not in text:
  raise SystemExit(f'Recorded behavior was not reproduced: {marker}')
print('Permission, request-context, and Kafka-default observations reproduced.')
PY
printf 'Reviewed commit: %s\nSource: %s\nEvidence: %s\n' "$review_commit" "$review_run/source" "$review_run/evidence"
printf '%s\n' 'Passing checks reproduce the recorded defects and defaults. They do not certify production security.'
