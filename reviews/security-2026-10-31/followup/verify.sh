#!/usr/bin/env bash
set -euo pipefail
review_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(git -C "$review_dir" rev-parse --show-toplevel)
review_commit=6c233af6e06e0a404dbc353310dab2c886de0b45
review_run=$(mktemp -d /tmp/funnel-security-followup.XXXXXX)
mkdir -p "$review_run/source" "$review_run/evidence" "$review_run/checks"
git -C "$repo_dir" archive "$review_commit" | tar -x -C "$review_run/source"
cp "$review_dir/checks/cancel_policy_test.go.txt" "$review_run/source/server/cancel_policy_review_test.go"
cp "$review_dir/checks/upload_source_test.go.txt" "$review_run/source/storage/source_type_review_test.go"
for check in cleanup event_logging config_redaction; do
  cp "$review_dir/checks/${check}_check.go.txt" "$review_run/checks/$check.go"
done
export GOCACHE="${GOCACHE:-/tmp/funnel-security-gocache}"
export GOPROXY=off
cd "$review_run/source"
go test ./server ./storage -run '^TestReview(CancelPolicyTarget|UploadSourceCancellation)$' -v -count=1 -timeout=20s > "$review_run/evidence/policy-and-source.txt" 2>&1
for check in cleanup event_logging config_redaction; do
  go run "$review_run/checks/$check.go" > "$review_run/evidence/$check.txt" 2>&1
done
python3 - "$review_run/evidence" <<'PY'
from pathlib import Path
import sys
root = Path(sys.argv[1])
checks = {
 'policy-and-source.txt': [
  'cancel_policy_called=true action=CANCEL target_task_missing=true',
  'canceled_upload_pending_on_nonregular_source=true',
  'operation_finished_after_fixture_release=true returned_error=true',
 ],
 'cleanup.txt': ['unrelated_prefix_job_removed=true', 'external_idle_shared_sa_removed=true'],
 'event_logging.txt': [
  'task_created_info_env_retained=true', 'task_created_info_signed_url_retained=true',
  'transfer_info_signed_url_retained=true', 'failed_event_error_env_retained=true',
  'failed_event_error_signed_url_retained=true',
 ],
 'config_redaction.txt': [
  'safe_container_env_retained=true', 'safe_swift_auth_url_password_retained=true',
  'safe_swift_auth_url_query_retained=true', 'safe_explicit_password_redacted=true',
  'safe_plugin_param_redacted=true',
 ],
}
for name, markers in checks.items():
 text = (root / name).read_text()
 for marker in markers:
  if marker not in text:
   raise SystemExit(f'Recorded behavior not reproduced: {name}: {marker}')
print('All follow-up boundary checks reproduced.')
PY
printf 'Reviewed commit: %s\nSource: %s\nEvidence: %s\n' "$review_commit" "$review_run/source" "$review_run/evidence"
printf '%s\n' 'Checks reproduce recorded failures, not a passing production security assessment.'
