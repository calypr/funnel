# Cleanup ownership, upload sources, and policy coverage

Reviewed commit: `6c233af6e06e0a404dbc353310dab2c886de0b45`. This continues the [production gate review](../production/PRODUCTION-SECURITY-REVIEW.md) and [initial inventory](../SECURITY-AUDIT.md). No production code changed.

## Intent and result

Continue looking for important production security failures without repeating the same findings or exercising live systems. This pass adds **two root-cause findings**, P09 and P10. P09 has two resource-deletion variants. It also strengthens three existing findings: logging at ordinary levels (S15), incomplete safe-configuration redaction (S16), and incomplete plugin policy inputs (P08).

The evidence is narrower than a deployed compromise: fake-client resource deletion, in-memory logging/serialization, a recording policy plugin, and one bounded temporary-file operation. Actual cluster RBAC, live worker termination, cloud database authorization, and downstream exposure remain untested.

## Reproduce the bounded checks

From the repository root:

```sh
bash reviews/security-2026-10-31/followup/verify.sh
```

The script archives the reviewed commit to a temporary source tree. Tests and check programs are installed only there. It runs without network access, live services, containers, real credentials, or production resource deletion. The upload check releases its temporary fixture and waits for the operation to finish. Cached Go dependencies are required.

The checks assert the recorded failures are present. A passing run reproduces the observations; it does not establish a secure deployment. Individual commands below run from the script's printed source directory, with `CHECKS` set to its sibling `checks` directory. Go runs use `GOCACHE=/tmp/funnel-security-gocache GOPROXY=off`.

## P09. Cleanup decisions do not establish resource ownership

**Priority: high where cleanup shares a namespace with externally managed identities or other workloads.** This is one missing-ownership control with two independently checked deletion paths.

### Unrelated Jobs are selected by name prefix

Sources: `compute/kubernetes/resources/job.go:153-172`, `compute/kubernetes/cleanup.go:125`, `compute/kubernetes/backend.go:201-207`.

`DeleteExecutorJobs` lists every Job in the namespace. It selects names starting with `taskID + "-"` without checking the executor label, expected numeric suffix, task ownership, owner reference, or UID. The cleanup path can therefore delete an object that its own controller did not create or own.

The fake-client fixture contains a benign operator-owned Job named `task-a-operator-backup`, labeled `app=operator` and without a Funnel owner reference. Cleanup for `task-a` removes it.

### An externally managed shared ServiceAccount is deleted when idle

Sources: `compute/kubernetes/backend.go:280-305`, `compute/kubernetes/cleanup.go:140-154`, `compute/kubernetes/resources/serviceaccount.go:115-138`.

Creation explicitly distinguishes externally managed/shared accounts: an existing account is reused, and an external account is not owned by the worker Job. Cleanup later forwards its name with `SharedSA=true`. The helper checks whether a nonterminating Pod currently uses the account, then deletes it if none does. Current idleness does not establish controller ownership or permission to end the external account's lifecycle.

The fake fixture is a ServiceAccount labeled `owner=operator`, with no task owner and no active Pods. Cleanup removes it.

### Verification and impact boundary

Command:

```sh
go run "$CHECKS/cleanup.go"
```

Actual output:

```text
unrelated_prefix_job_removed=true
external_idle_shared_sa_removed=true
```

Required: preserve unrelated Jobs and externally managed ServiceAccounts; delete only resources whose lifecycle the task/controller actually owns. Namespace access, similar names, and absence of an active consumer do not supply that proof.

Preconditions for Job impact: another Job shares the prefix in the configured Jobs namespace; task cleanup executes; controller RBAC permits deletion. This does not demonstrate that tenants can choose generated task IDs or construct a cross-tenant collision.

Preconditions for ServiceAccount impact: an externally supplied account is used; the legacy storage path or configured account template causes this cleanup branch to execute; storage cleanup has no blocking error; the account has no nonterminating attached Pod; controller RBAC permits deletion. A new workload can still need an identity that is momentarily idle. Arbitrary account targeting and deployed permissions were not exercised.

The source has stronger ownership/UID-precondition checks for storage-plan PV deletion. This finding does not assert that all Kubernetes cleanup lacks ownership checks.

Reviewer: cleanup controls. Foreground reran both fake-client observations, then included them in the fresh-snapshot verification. Evidence: [cleanup output](evidence/cleanup.txt).

## P10. Upload source opening can remain blocked after cancellation

**Priority: production availability blocker for untrusted file outputs using the affected ordinary upload path.**

Sources: `storage/http.go:129-145`, `worker/storage.go:FlattenOutputs/UploadOutputs`, `storage/transfer.go:Upload`.

The ordinary HTTP uploader opens its source with `os.Open` before constructing the context-bound HTTP request or obtaining file information. A nonregular source can block at that opening step. Canceling the context does not interrupt that filesystem open, and an HTTP timeout cannot govern an HTTP operation that has not started. Output classification does not establish that a file-typed output is regular before it reaches this uploader.

The bounded test creates one benign temporary FIFO, starts a single upload with a transport that cannot perform network I/O, cancels the context, and observes that the operation remains pending. It then releases the FIFO and waits for the operation to finish. No worker process or service was hung.

Command:

```sh
go test ./storage -run '^TestReviewUploadSourceCancellation$' -v -count=1 -timeout=15s
```

Actual output:

```text
canceled_upload_pending_on_nonregular_source=true
operation_finished_after_fixture_release=true returned_error=true
```

Required: reject unsupported source types without blocking, and keep cancellation effective throughout source opening/publication. Type checks must remain valid at opening time rather than introduce a new check/open race. The repository already has nonblocking regular-file helpers for some mounted-storage operations; those protections do not cover this ordinary HTTP source open.

Preconditions for worker impact: a submitted workload can leave a nonregular object at a declared output, that output reaches the ordinary HTTP uploader, and the worker has not already skipped publication before entering it. The runtime check proves the adapter's cancellation gap, not a full worker/process failure or cluster-wide resource exhaustion. Other ordinary uploaders also have plain opens by source inspection, but only HTTP was verified in this pass.

This is independent of P06's symlink confinement issue: a source can be inside the correct root and still block the worker because it is not a regular file.

Reviewer: foreground. Evidence: [bounded source test](evidence/policy-and-source.txt).

## S15 extension. Secrets reach INFO and ERROR logs, not only global debug output

**Priority: high when application logs have broader access or retention than job credentials.** This extends the existing logging root cause instead of being counted as another vulnerability for each sink.

Sources: `events/err_logger.go:17-20`, `events/log_writer.go:66-72`, `server/tes.go:165`, `cmd/server/run.go:158-163,278`, `cmd/worker/run.go:50-61,234-235`, `worker/storage.go:181-219`.

Three ordinary logging paths were checked against actual loggers and synthetic data:

- `ErrLogger` logs the full event data at ERROR when an underlying writer returns an error. Server and worker assembly install this wrapper independently of whether the `log` event writer is enabled. A failed task-created write can therefore serialize executor environment credentials and signed input/output URLs even at error-only log level.
- A configured Slurm server log event writer logs task-created events wholesale at INFO. Other server compute modes skip that particular `log` writer; this is not unconditional default server behavior.
- The worker's normal log event writer receives INFO transfer messages containing full URLs. Defaults include that writer. Signed URL credentials remain in the log fields.

Command:

```sh
go run "$CHECKS/event_logging.go"
```

Actual output flags:

```text
task_created_info_env_retained=true
task_created_info_signed_url_retained=true
transfer_info_signed_url_retained=true
failed_event_error_env_retained=true
failed_event_error_signed_url_retained=true
```

The error-only logger captured synthetic executor environment and signature values inside `event_data`. The transfer logger captured a synthetic signature inside `url`.

Required: use a consistent secret-safe event representation at every log level and on failures. Lowering debug verbosity alone does not close S15. This does not establish that existing production logs contain credentials: no existing logs were read, and the writer failure was synthetic.

Reviewer: secret boundaries; foreground reran the actual in-memory logger check. Evidence: [event logging output](evidence/event_logging.txt).

## S16 extension. Safe configuration retains environment values and credential-bearing AuthURL

Sources: `config/safe.go`, `config/config.proto:ContainerConfig.Env`, `storage/swift.go:34`.

Command:

```sh
go run "$CHECKS/config_redaction.go"
```

Actual output:

```text
safe_container_env_retained=true
safe_swift_auth_url_password_retained=true
safe_swift_auth_url_query_retained=true
safe_explicit_password_redacted=true
safe_plugin_param_redacted=true
```

The check calls actual `Config.Safe()` followed by `config.ToYaml`. The supported worker-container environment map survives unchanged. Swift's explicit `Password` is redacted, but userinfo/query values in `AuthURL` remain. Safe output can therefore retain credentials when operators use these representations.

Required: safe configuration output excludes secret environment values and credential-bearing structured URL components. Preconditions include storing a secret in those fields and exporting/logging the safe configuration. No Swift authentication or actual credential disclosure was tested. This expands the structured-secret redaction root cause already demonstrated with MongoDB URI credentials in S16.

Reviewer: secret boundaries; foreground reran serialization. Evidence: [configuration output](evidence/config_redaction.txt).

## P08 extension. Cancellation policy does not receive the target task

**Priority: high if the plugin is relied on for object-level cancellation authorization.** Database or gateway authorization may independently constrain the action.

Sources: `server/tes.go:246-249`, `server/tes.go:DoPluginAction`, `plugins/shared/interface.go:Authorize`.

`CancelTask` receives a target task ID but calls `HandleDoPluginAction` with a nil task. The plugin receives action `CANCEL` without that target in the defined task argument. Request headers could carry additional information in a particular deployment, but this service does not supply the target through its policy interface.

The bounded test uses a recording plugin and a synthetic terminal-task reader. It performs no state change, cleanup, or provider operation.

Command:

```sh
go test ./server -run '^TestReviewCancelPolicyTarget$' -v -count=1
```

Actual output:

```text
cancel_policy_called=true action=CANCEL target_task_missing=true
```

Required: a policy responsible for deciding access to an object receives the authenticated actor and the specific object being requested. This does not demonstrate unauthorized cancellation in a deployment whose database/gateway enforces ownership. It establishes that merely enabling this plugin does not provide a complete per-object read/cancel authorization boundary. That is the same incomplete policy-integration root cause as P08, not an independent count.

Reviewer: foreground. Evidence: [policy test](evidence/policy-and-source.txt).

## Deferred validation concern

Source inspection found that `events.Service.WriteEvent` forwards events without checking event-type/payload consistency, while backend branches assume corresponding payloads exist. This is a candidate input-validation/availability problem. The validation reviewer stopped at a cybersecurity safeguard before completing a report that the foreground could verify. Its partial runtime claims and artifacts were not rerun or promoted into a confirmed finding. No server crash is claimed here.

A completed review must distinguish ordinary rejected input, a recovered request failure, and an uncaught service failure. This gap remains open; it is not a passing validation result.

## Exclusions and coverage

The configured three-seat interrogate panel was used with separate cleanup, secret-boundary, and validation briefs, all `gpt-6.1-sol` at low effort. Cleanup and secret reviewers completed. Validation stopped; its complete exclusion count is unavailable. The foreground reran every surviving checked observation and added the bounded policy/source checks. No fixes were applied.

Explicit exclusions:

- Cleanup: four broader claims were withheld or disproved. Storage-plan PV cleanup verifies labels/recorded Job UID and uses a PV UID delete precondition. External PVC deletion was not demonstrated. Database errors cause cleanup eligibility checks to fail closed. A separate legacy-PV finalizer/UID failure was not reproduced. Existing P02/P03 were not recounted.
- Secret boundaries: full worker ConfigMaps were not promoted without an unauthorized reader; HPC config files use `0600`; trusted plugin environment inheritance did not establish a new untrusted boundary. Existing debug/transport/FTP/plugin findings were not recounted. Logging variants and safe-configuration variants remain grouped.
- Validation: no independently verified new finding from that reviewer survived this pass. Source concerns remain above, with no executable blocked fixture published as verified evidence.

Cloud-database event authorization remains incompletely verified. This pass did not retry blocked database/disclosure fixtures or extend into live cloud, cluster, network, or production-log testing. It adds meaningful evidence and lifecycle coverage; it does not establish an exhaustive vulnerability list.
