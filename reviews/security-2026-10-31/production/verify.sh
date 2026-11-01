#!/usr/bin/env bash
set -euo pipefail
review_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(git -C "$review_dir" rev-parse --show-toplevel)
review_commit=6c233af6e06e0a404dbc353310dab2c886de0b45
review_run=$(mktemp -d /tmp/funnel-production-review.XXXXXX)
mkdir -p "$review_run/source" "$review_run/evidence"
git -C "$repo_dir" archive "$review_commit" | tar -x -C "$review_run/source"
cp "$review_dir/checks/postgres_context_test.go.txt" "$review_run/source/database/postgres/security_context_audit_test.go"
cp "$review_dir/checks/defaults_test.go.txt" "$review_run/source/cmd/server/security_defaults_audit_test.go"
cp "$review_dir/checks/input_staging_test.go.txt" "$review_run/source/worker/security_input_audit_test.go"
cp "$review_dir/checks/output_policy_test.go.txt" "$review_run/source/worker/security_output_policy_audit_test.go"
cp "$review_dir/checks/plugin_policy_test.go.txt" "$review_run/source/server/security_plugin_policy_audit_test.go"
cp "$review_dir/checks/metadata_test.go.txt" "$review_run/source/tes/security_metadata_audit_test.go"
cp "$review_dir/checks/aws_cancellation_test.go.txt" "$review_run/source/compute/aws_batch/security_cancel_audit_test.go"
cp "$review_dir/checks/platform_probe.go.txt" "$review_run/platform_probe.go"
export GOCACHE="${GOCACHE:-/tmp/funnel-security-gocache}"
export GOPROXY=off
cd "$review_run/source"
go test ./database/postgres ./cmd/server ./server ./worker ./tes ./compute/aws_batch -run '^TestAudit' -v -count=1 > "$review_run/evidence/local-checks.txt" 2>&1
go run "$review_run/platform_probe.go" > "$review_run/evidence/platform-checks.txt" 2>&1
python3 - "$review_run/evidence" <<'PY'
from pathlib import Path
import sys
root = Path(sys.argv[1])
checks = {
 'local-checks.txt': [
   'incoming_admin=false incoming_all_tasks=false',
   'database_context_user=SYSTEM database_admin=true database_all_tasks=true',
   'rpc_bind=:9090 wildcard_ip=true basic_credentials=0 oidc_configured=false',
   'no_credentials_public=true all_tasks=true other_owner_accessible=true',
   'source_and_staged_input_same_inode=true executor_input_mount_readonly=false',
   'outside_workspace_symlink_accepted_for_upload=true',
   'client_logs_retained=true client_backend_id_retained=true',
   'get_error=<nil> list_error=<nil> authorizer_calls_for_reads=0',
   'create_rejected=true authorizer_calls_after_create=1',
   'cancel_response_error=<nil> final_task_state=CANCELED provider_cancel_calls=0',
   'direct_queued_cancel_provider_calls=1',
 ],
 'platform-checks.txt': [
   'unknown field "spec"',
   'automount_pointer_nil=true',
   'worker-job.yaml sa=funnel-sa-synthetic pod_automount_pointer_nil=true',
   'executor-job.yaml sa=funnel-sa-synthetic pod_automount_pointer_nil=true',
   'reserved_identity_tag_validation_errors=0',
   'template_variable_identity_selected=preexisting-privileged-sa',
 ],
}
for name, markers in checks.items():
 text = (root / name).read_text()
 for marker in markers:
  if marker not in text:
   raise SystemExit(f'Recorded behavior not reproduced: {name}: {marker}')
print('All recorded defensive boundary checks reproduced.')
PY
printf 'Reviewed commit: %s\nSource: %s\nEvidence: %s\n' "$review_commit" "$review_run/source" "$review_run/evidence"
printf '%s\n' 'These checks reproduce boundary failures; passing is not a production-security approval.'
