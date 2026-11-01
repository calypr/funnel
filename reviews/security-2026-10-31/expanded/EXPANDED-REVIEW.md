# Expanded source and configuration security review

The user wants additional production-relevant security issues with evidence. This pass reviews commit `6c233af6e06e0a404dbc353310dab2c886de0b45` and preserves the earlier finding IDs. It adds two verified defects, P11 and P12. Kafka configuration extends S09. Other observations remain source concerns.

## Overview

P11 changes the process-wide file permission policy during directory creation. P12 fails to attach the caller's cancellation context to a Google Cloud Storage upload request. Their practical impact depends on deployment permissions and upload timing. P11 is a permission-policy defect. P12 is an operational cancellation defect relevant to reliable revocation, not an authorization bypass.

The cloud control-file and terminal-rendering observations remain source-only concerns under the narrowed scope. This report adds no foreground runtime claim for either observation. It does not claim exhaustive coverage.

The [production assessment](../production/PRODUCTION-SECURITY-REVIEW.md) and [cleanup and logging follow-up](../followup/FOLLOWUP-REVIEW.md) remain relevant. The [coverage table](coverage.tsv) records the assigned areas, owners, prior findings, evidence, and remaining gaps.

## Key concepts

A process umask affects subsequent file creation throughout that process. Goroutines share that state. A directory helper must not silently weaken the caller's policy for unrelated file operations.

A context-aware upload body can interrupt reads. Canceling the HTTP operation also requires the request itself to carry the caller's context. Body cancellation and request cancellation are separate controls.

Configured event writers are active outbound paths. Reader functions alone do not establish a production inbound path. A hypothetical external consumer cannot be treated as a verified database entry point.

## How it works

### P11. Directory creation weakens later file permissions

**Remediation status:** Fixed in the working tree on 2026-11-01. Removed the process-wide umask change. The new isolated `util/fsutil/dir_test.go` regression failed before the fix and passes afterward, checking both directory permissions and an unrelated file. `go test -race ./util/fsutil -count=1` passes. The evidence below describes the original defect.

Source is `util/fsutil/dir.go:22-36`. `EnsureDir` calls `syscall.Umask(0000)` when its target does not exist and never restores the previous mask. `EnsurePath` reaches the same behavior.

The foreground check observed:

```text
caller_umask_before=0077 caller_umask_after=0000 subsequent_file_mode=0666
```

The test sets a restrictive caller mask, calls the actual helper on one new temporary directory, and creates an unrelated ordinary file. It reads that file's actual mode. The test restores the mask before its process exits. The temporary parent itself is private, so the check does not expose data to another user.

Required behavior is to preserve the caller's process permission policy. A file created with mode `0666` under mask `0077` would otherwise receive `0600`. The helper changes the result to `0666`.

The HPC submission path is one concrete production caller. It calls `EnsureDir` before `os.Create` creates its submission file in `compute/hpc_backend.go:231-247`. Input staging also reaches `EnsurePath` before ordinary file creation. Other local users could read or alter affected files if parent directories permit access and the files use permissive creation modes.

The check establishes permission weakening. It does not establish cross-user access on a deployed filesystem. Files explicitly created with mode `0600` remain `0600` under a zero mask. This does not overturn that narrower control observed in the earlier HPC configuration review.

Priority is high on shared hosts or shared filesystems with traversable parent directories. The code-level policy change exists regardless of whether the deployment exposes the resulting files.

### P12. Google Cloud Storage upload requests lose caller cancellation

Source is `storage/google_cloud.go:158-180`. `GoogleCloud.Put` wraps its file reader with `fsutil.Reader(ctx, reader)` but invokes the generated insert request without attaching that context. The reader's cancellation checks in `util/fsutil/io.go:15-42` limit the defect but do not cancel HTTP work that no longer needs another body read.

The foreground recording transport observed:

```text
outbound_calls=1 parent_canceled=true outbound_request_canceled=false returned_error=true
```

The recording transport cancels the parent synchronously after it receives the request, inspects both contexts, closes the synthetic request body, and returns a synthetic error. No request is sent over a network. This proves the outgoing request lacks the caller's cancellation signal. The returned error comes from the recording transport. It does not prove a completed remote upload after cancellation or a live worker hang.

Required behavior is for the outbound upload request to inherit the caller's cancellation and deadline. Task output publication reaches the storage adapter through `UploadOutputs` in `worker/storage.go:134` and the upload loop in `storage/transfer.go:74`. In a deployment using this backend, an in-flight request may continue consuming resources or finish remote work after caller cancellation, depending on timing and transport behavior.

Priority is medium for deployments requiring reliable cancellation. This differs from P10, where source-file opening blocks before an HTTP request exists. It also differs from GCP Batch's no-op execution cancellation in S19. The omitted request context is a separate adapter defect with its own acceptance condition.

### S09 extension. Kafka uses default transport security settings

`NewKafkaWriter` in `events/kafka.go:19` passes a nil Sarama configuration. `Kafka` configuration in `config/config.proto` exposes servers and topic, with no TLS or SASL settings. The pure library-default check observed:

```text
kafka_default_tls=false kafka_default_sasl=false
```

This run verifies the library defaults. Source connects those defaults to the application's constructor. It does not capture broker traffic or establish deployed exposure. Kafka is opt-in. An independently secured connection or network boundary may protect a deployment.

If configured Kafka traffic crosses an unprotected boundary, this extends the missing transport-policy control already recorded as S09. It is not a new root-cause count. The foreground did not repeat the explorer's task-content serialization check.

## Where things live

| Area | Source | Current result |
| --- | --- | --- |
| File permissions | `util/fsutil/dir.go:22` | P11 reproduced with one temporary directory and file |
| Production permission caller | `compute/hpc_backend.go:231` | Source trace to submission-file creation |
| Google upload request | `storage/google_cloud.go:158` | P12 reproduced with a recording transport |
| Cancellation during body reads | `util/fsutil/io.go:15` | A separate control remains present |
| Kafka publication | `events/kafka.go:19` | S09 extension from default configuration and source |
| Broker subscriptions | `events/kafka.go:66`, `events/pubsub.go:96` | No production reader caller found in this checkout |
| Terminal task rows | `cmd/termdash/view.go:104` | Source concern, no foreground terminal-control test retained |
| HPC control-file separation | `compute/hpc_backend.go:266`, `worker/file_mapper.go:355` | Source concern, broader verification not retained |

## Reproduce the retained checks

From the repository root:

```sh
bash reviews/security-2026-10-31/expanded/verify.sh
```

The script archives the reviewed commit to a fresh temporary source directory. It installs only the three checks in [checks](checks/). It runs package tests for permission policy, Google request cancellation, and Kafka defaults. It uses cached dependencies, temporary files, disabled module fetching, and a recording transport that intercepts upload requests. The script excludes the broader cloud control-file, terminal-control, database-protocol, malformed-event, and data-disclosure fixtures.

Individual commands run from the script's printed source directory:

```sh
GOCACHE=/tmp/funnel-security-gocache GOPROXY=off go test ./util/fsutil -run '^TestReviewDirectoryPreservesCallerPermissions$' -v -count=1
GOCACHE=/tmp/funnel-security-gocache GOPROXY=off go test ./storage -run '^TestReviewGoogleUploadContext$' -v -count=1
GOCACHE=/tmp/funnel-security-gocache GOPROXY=off go test ./events -run '^TestReviewKafkaConnectionDefaults$' -v -count=1
```

[The retained output](evidence/checks.txt) records the actual observations. Passing these audit checks means the recorded bad behavior or defaults were reproduced on this frozen commit. It does not certify a secure deployment or act as a regression suite for future fixes.

## Gotchas and source concerns

The broker explorer attempted existing Pub/Sub fake-server tests. Sandbox rules rejected localhost binding before a handler ran. The foreground did not retry them. This is an unverified path, not a passing test.

The cloud and client explorers assigned stronger verification status to their observations than the foreground retained. The user's narrower constraints in [SESSION-NOTES.md](SESSION-NOTES.md) govern the final result. Their broader fixtures are excluded from the rerun script and the confirmed-finding count.

- Worker control files and task volumes share a filesystem hierarchy in the inspected HPC path. The placement and mapping relationship merits an authorized isolation check. No control-file disclosure or executor mount was reproduced by the foreground in this pass.
- Task names reach the terminal dashboard's row renderer without explicit display escaping in that function. The full terminal-output behavior remains unverified here. No command execution, terminal data access, or clipboard effect is claimed.
- Administrative clients use default redirect handling. A trusted-server redirect can retain credentials for certain destinations under Go's policy, but the source review did not establish an untrusted redirect trigger in this repository. The HTTPS-to-HTTP downgrade concern remains part of S09, without a new foreground credential test.
- `/login/token` is outside the root handler's cache-control wrapper. Browser and proxy caching remain untested, and the OIDC startup defect S12 remains relevant. This is not promoted into a demonstrated token disclosure.
- Pub/Sub publication returns before delivery acknowledgment is checked by the caller. That is a source-level delivery limitation. Whether this stream is an authoritative audit record is a deployment policy that was not established.
- Broker readers accept caller-supplied writers, but this repository has no non-test reader call site in the inspected paths. Missing message actor/replay fields therefore remain an external integration question, not a verified privileged database path.

## Coverage, exclusions, and principles

Fresh configured explorers covered cloud/HPC, broker events, and administrative clients. The foreground covered storage and permissions, reran the retained checks, and grouped duplicate root causes. The how explainer checked source and synthesized the component flows. The review also retains the earlier cloud-database, live IAM, admission/RBAC, network exposure, and race-condition gaps.

Explicit exclusions include unsupported custom HPC templates, actor assumptions for external broker consumers, delivery completeness without an audit requirement, untrusted redirect behavior without a demonstrated trigger, and source concerns whose runtime verification was stopped or not retained. Existing S01, S19, P07, S09, P08, and metadata-provenance findings were not recounted.

Prove It Works required foreground observations and a fresh-snapshot rerun before retaining P11/P12. Build the Lever produced `verify.sh`, the stored checks, and the coverage table. Test Behavior, Not Implementation selected actual subsequent file permissions and outbound request cancellation as observable outcomes. The decision trail records the change in scope after the user's reported blocker. These principles were read in this pass.

No product fixes, live-system access, real credential reads, network probes, container execution, external publication, or commits occurred. Additional issues may remain. This is an expanded evidence inventory, not an exhaustive vulnerability claim.

## Decision-trail review

The configured fresh `gpt-6.1-sol` reviewer checked the trail, retained checks, source, and report without rerunning code. It found that a statement about disabled networking was too broad. The report now says that module fetching is disabled and upload requests are intercepted by a recording transport.

The reviewer confirmed that P11/P12 match the saved output and that the broader cloud and terminal observations remain source-only. An external transcript was not supplied to the reviewer, so it could not independently establish historical execution chronology or foreground authorship from the files alone. The foreground checked the trail against this conversation. The rerun script provides a way to verify current behavior without relying on that chronology.

Material limits remain deployed filesystem access, actual remote upload completion after cancellation, broker exposure, live IAM/RBAC, network policy, terminal behavior, and race conditions.
