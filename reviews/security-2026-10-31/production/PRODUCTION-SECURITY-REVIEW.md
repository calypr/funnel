# Production security gate review

Expanded checks add [permission-policy P11 and request-cancellation P12](../expanded/EXPANDED-REVIEW.md). Broader observations remain source-only under the narrowed scope.

Further evidence: [cleanup ownership, upload-source cancellation, and ordinary-level logging](../followup/FOLLOWUP-REVIEW.md), including new P09/P10 and extensions of P08/S15/S16.

Commit: `6c233af6e06e0a404dbc353310dab2c886de0b45`. This supplements the [initial inventory](../SECURITY-AUDIT.md); it does not replace its evidence or claim exhaustive coverage.

**Assessment:** I would block deployment that exposes this service to mutually untrusted tenants or runs untrusted submitted code with sensitive worker credentials. There are failures in tenant identity, execution-spec construction, filesystem separation, infrastructure authority, and credential handling. Some deployed controls can mitigate particular paths; those controls have not been inspected here. This is a code and supplied-template assessment, not a statement that a live environment has been compromised.

## Intent and threat model

Find the security failures that matter to a production organization, prioritizing unauthorized data access, infrastructure privilege, shared-data integrity, reusable credentials, and reliable execution revocation. Do not apply fixes or exercise production systems.

The principal is a legitimate tenant allowed to submit a job and access their own task data. That permission does not grant server identity, another tenant's data, shared dataset modification, arbitrary service accounts, or infrastructure administration. Submitted container code and its output files are untrusted. Server, plugins, and worker control logic are trusted, but their per-request authority must remain separate.

Single-user development, a closed pool of fully trusted operators, and externally enforced tenant authorization are different deployment models. A restriction enforced by a plugin, proxy, admission controller, filesystem, or IAM policy must be demonstrated rather than assumed.

## Release gates

These rows group root causes and security requirements. They are not an inflated count of independent vulnerabilities.

| Production requirement | Assessment at reviewed commit | Evidence |
| --- | --- | --- |
| Preserve tenant identity through authorization | **Fail** for PostgreSQL read methods; plugin read-policy gap when plugin is the intended authority | P01, P08; prior S05/S06 independently demonstrate event mutation/replacement failures |
| Treat task fields as data when building execution specs | **Fail** in inspected Docker/GCP option construction and bundled Kubernetes executor template | Prior S01/S02 |
| Keep task files separate from worker files and original input data | **Fail** for local prefix policies and staged-file independence; output link boundary is missing in inspected flow | Prior S03/S04; P05/P06 |
| Keep job submitters separate from infrastructure identities | **Conditional fail** for supplied Kubernetes identities and user-controlled identity tag; scheduler exposes administrative writes to ordinary accounts | P02/P03; prior S07 |
| Require deliberate authenticated network exposure | **Fail by default:** wildcard listener construction with no credentials and public task access | P04; actual firewall/proxy reachability untested |
| Protect reusable credentials in transit and logs | **Fail** in credential-bearing plaintext modes, FTP host binding, SFTP handling, and debug/redaction paths | Prior S09-S11, S15-S17 |
| Revoke work rather than only change its database status | **Fail** for the inspected AWS cancellation sequence and GCP no-op | P07; prior S19 |
| Initialize and enforce the selected authentication policy | **Fail** for OIDC initialization; audience/expiry/session-state gaps are latent behind that failure | Prior S12-S14 |
| Bound work before unauthenticated requests consume resources | **Fail** at the HTTP gateway unless upstream limits compensate | Prior S18 |
| Use a toolchain without the identified reachable advisory | **Fail** for the pinned Go 1.27.2 in the earlier fresh scan | Prior D01; no new advisory scan in this pass |

For Kubernetes, disabling token automount must apply to the selected ServiceAccount or Pod. The official [service-account configuration documentation](https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/) describes field placement and Pod precedence. The [application security checklist](https://kubernetes.io/docs/concepts/security/application-security-checklist/) recommends disabling tokens for workloads that do not need API access. These are supporting requirements; the template failures below were checked against actual decoded objects.

## Verification method

Run from the repository root:

```sh
bash reviews/security-2026-10-31/production/verify.sh
```

The script archives this exact commit into a new temporary source directory and installs review-only unit tests there. It runs deterministic tests with synthetic fixtures, a recording authorizer, fake provider/Kubernetes clients, and configuration/schema checks. It does not open network listeners, query live databases, launch jobs, contact cloud services, read worker credentials, or modify production source. It requires locally cached Go dependencies. The printed source/evidence directories support rerunning individual checks.

Tests assert that the recorded defects are still observable. A passing script is a successful reproduction, not an assertion that the service is secure. Source-level consequences and missing end-to-end evidence are explicit below.

## New findings and deeper evidence

### P01. PostgreSQL read authorization loses the authenticated identity

**Priority: release blocker for tenant isolation.**

Sources: `database/postgres/tes.go:43-66` and `:95-121`; `database/postgres/new.go:75-77`; `server/auth.go:GetUser`.

Both `GetTask` and `ListTasks` replace the incoming context with `db.context()`. That helper derives from `context.Background()` rather than the caller. The subsequent ownership checks use the replacement context. `GetUser` interprets missing identity as the privileged internal `SYSTEM` principal.

Check:

```sh
go test ./database/postgres -run '^TestAuditRequestIdentityRetention$' -v -count=1
```

Actual output:

```text
incoming_user=synthetic-tenant incoming_admin=false incoming_all_tasks=false
database_context_user=SYSTEM database_admin=true database_all_tasks=true
```

Required: the database operation retains the tenant principal and its ownership restrictions. The local check verifies the actual helper and actual authentication/user-policy functions without a database. Source tracing establishes that these read methods use that helper before their authorization decision. The foreground did not run a cross-tenant database query or establish actual deployed data exposure. PostgreSQL execution claims use a caller-derived timeout and are a different, better-protected path.

**Correction to the first inventory:** seeing owner checks in a database implementation does not establish isolation. PostgreSQL's checks use the wrong principal. The earlier broad observation about protected read paths must not be read as a passing PostgreSQL authorization result.

Reviewer: API identified; foreground reproduced the context failure with a bounded check. The API worker's unfinished database fixtures were not used as proof.

### P02. Supplied Kubernetes templates do not enforce their intended identity/token boundary

**Priority: release blocker for deployments adopting these templates with an infrastructure-capable shared identity.**

Sources: `config/kubernetes/serviceaccount.yaml:13-15`; `config/kubernetes/worker-job.yaml:58`; `config/kubernetes/executor-job.yaml:48`; `compute/kubernetes/resources/serviceaccount.go:55`.

The supplied ServiceAccount template places `automountServiceAccountToken: false` beneath `spec`, but this field belongs at the ServiceAccount's top level. Strict decoding rejects the document; production permissive decoding discards the unknown field. Furthermore, the ServiceAccount created by that template is task-specific while both supplied Jobs select a different shared ServiceAccount. Neither Job specifies Pod-level token disabling.

Check: `go run <review-run>/platform_probe.go`.

```text
serviceaccount_strict_decode_error=error unmarshaling JSON: while decoding JSON: json: unknown field "spec"
created_sa=funnel-worker-sa-synthetic-synthetic-task automount_pointer_nil=true
worker-job.yaml sa=funnel-sa-synthetic pod_automount_pointer_nil=true
executor-job.yaml sa=funnel-sa-synthetic pod_automount_pointer_nil=true
```

Required: Jobs use their intended identities and effective token policy is explicit on the selected object. Preconditions: these templates or equivalent derivatives are used; the shared ServiceAccount exists; admission and that ServiceAccount permit token mounting. If the shared account has no useful privileges, the privilege impact is reduced. If it does not exist, startup can fail. No cluster token mount or effective RBAC was exercised.

Reviewer: platform; foreground reran the saved check.

### P03. A task tag selects the worker's ServiceAccount without an internal authorization boundary

**Priority: high, conditional on an operator template consuming `ServiceAccountName` and lack of an external allowlist.**

Sources: `compute/kubernetes/resources/job.go:90-95`, `compute/kubernetes/resources/serviceaccount.go:35-46`, `tes/validate.go:Tags`.

`_WORKER_SA` is accepted as an ordinary task tag and copied into the template variable controlling worker identity. The production resource creator then uses the rendered identity. This crosses the job-data/authority boundary when task submitters can choose that field without a plugin/admission restriction.

Check: same platform probe, using a fake Kubernetes client and a synthetic custom template.

```text
reserved_identity_tag_validation_errors=0
template_variable_identity_selected=preexisting-privileged-sa
```

The name is a synthetic fixture; no existing service account or cluster was accessed. Required: worker identity is chosen by a trusted mapping or checked against the caller's explicit entitlement. Preconditions include a template consuming the variable and control-plane permission/admission allowing that account. The bundled templates currently ignore the variable, so this is a different deployment path from P02. `_FUNNEL_WORKER_ROLE_ARN` also reaches an annotation by source inspection, but IAM trust remains an independent boundary and no IAM role assumption is claimed.

Reviewer: platform; foreground reran the fake-client check.

### P04. `localhost` defaults do not imply a loopback-only authenticated service

**Priority: production exposure blocker if either listener is reachable beyond the intended trust boundary.**

Sources: `config/default.go:26-33`, `cmd/server/run.go:290`, `server/server.go:httpServer`, `server/auth.go:Interceptor`.

The configured host is advertised as `localhost`, but the assembled RPC listener uses `:9090`, which selects a wildcard address. HTTP constructs its address similarly. Defaults have no Basic/OIDC credentials and task access is `All`.

Check:

```sh
go test ./cmd/server -run '^TestAuditDefaultExposurePolicy$' -v -count=1
```

```text
advertised_host=localhost rpc_bind=:9090 wildcard_ip=true basic_credentials=0 oidc_configured=false
no_credentials_public=true all_tasks=true other_owner_accessible=true
```

This uses the real server assembler with an isolated temporary BoltDB and a no-op compute backend, then checks authentication policy and address resolution. It does not bind a listener or submit a job. Required: exposing a listener beyond loopback with unrestricted access is deliberate, explicit, and separately authorized. A production proxy/firewall can reduce exposure, but `HostName` alone does not. This promotes the first inventory's default-exposure concern into a reproduced configuration-policy result, not a claim about actual internet reachability.

Reviewer: foreground.

### P05. Local input staging retains the source inode while granting a writable executor mount

**Priority: release blocker where shared/original datasets are authorized only as inputs.**

Sources: `storage/local.go:processFile`, `worker/file_mapper.go:398-400`, `worker/file_mapper.go:consolidateVolumes`.

Local storage first attempts to hardlink the input source into the task workspace. Input mounts are writable. The staged pathname can therefore be another name for the original source inode, rather than an independent working copy.

Check:

```sh
go test ./worker -run '^TestAuditInputStagingIndependence$' -v -count=1
```

```text
source_and_staged_input_same_inode=true executor_input_mount_readonly=false
```

Required for input-only access: task-local edits cannot change the original shared object. The check maps and stages one benign temporary fixture, compares inode identity, and inspects the resulting mount flag. It does not modify the source through the staged file or run a container. Preconditions: local storage can hardlink across these paths, permissions permit writes through the mounted inode, and the security contract distinguishes input reads from explicit output writes. If all callers already have unrestricted write access to the same source, this does not add authority. It is independent of the prefix-matching defects in S03/S04.

Reviewer: storage identified; foreground verified the narrower staging property. The stopped worker's data-modification probe was not run.

### P06. Output classification accepts a link whose target is outside the task workspace

**Priority: release blocker until output publication is shown to enforce a rooted source boundary for untrusted task files.**

Sources: `worker/storage.go:FlattenOutputs`, `worker/storage.go:fixLinks`, `worker/worker.go:processOutputs`; ordinary upload opens at `storage/http.go:135`, `storage/generic_s3.go:469`, `storage/amazon_s3.go:262`.

The file-output branch uses `os.Stat` and retains the symlink pathname as an upload source. `fixLinks` repairs some missing container paths but is not an outside-root rejection. The inspected ordinary storage uploaders subsequently open the source pathname using worker-process privileges.

Check:

```sh
go test ./worker -run '^TestAuditOutputLinkClassification$' -v -count=1
```

```text
outside_workspace_symlink_accepted_for_upload=true
```

Required: an untrusted output is resolved and opened within its authorized task root, including at open time. The check uses a benign fixture and symlink entirely under a temporary test directory. It verifies classification only: no upload, sensitive-file read, or external disclosure was performed. The later following-open behavior is source traced. Preconditions for disclosure would include a worker-readable target outside the task root and an output destination accessible to the submitter. Mounted-destination atomicity checks do not establish source-file confinement.

Reviewer: storage identified; foreground verified the narrower classification property. The stopped worker's disclosure probe was not run.

### P07. AWS cancellation ordering suppresses provider cancellation

**Priority: execution-revocation and cost-control blocker for AWS Batch.**

Sources: `server/tes.go:290-305`, `compute/aws_batch/backend.go:149-168`.

The task service changes database state to `CANCELED` before dispatching the compute cancellation event. The AWS backend reads that state and returns without a provider call unless it is still `QUEUED`.

Checks:

```sh
go test ./compute/aws_batch -run '^TestAudit(CancellationDispatchOrder|QueuedCancelControl)$' -v -count=1
```

```text
cancel_response_error=<nil> final_task_state=CANCELED provider_cancel_calls=0
direct_queued_cancel_provider_calls=1
```

Required: a successful revocation flow dispatches the applicable provider operation, or clearly records/report its failure. The test calls the real task-service cancellation path and real AWS backend with a recording provider stub and synthetic state store. The control confirms direct queued cancellation reaches the same provider stub. No AWS job exists in this test. Worker polling might later stop a task once a worker starts; the result does not establish indefinite execution. It establishes that the API success is not evidence that the cloud queue/job was canceled. This is distinct from GCP's unconditional no-op in S19.

Reviewer: foreground.

### P08. Enabling an authorizer plugin does not apply it to task reads

**Priority: release blocker if a deployment relies on that plugin for per-user read authorization.**

Sources: `server/tes.go:214-245`, `plugins/proto/plugin.proto:Type`.

The plugin authorization block in `GetTask` is commented out. `ListTasks` calls the database reader directly. The plugin interface explicitly has a `GET` action, but enabling the plugin does not make it a read-enforcement boundary. Create and cancel paths invoke it.

Check:

```sh
go test ./server -run '^TestAuditPluginReadPolicyInvocation$' -v -count=1
```

```text
get_error=<nil> list_error=<nil> authorizer_calls_for_reads=0
create_rejected=true authorizer_calls_after_create=1
```

A recording authorizer denies every request. A benign synthetic reader supplies no real records. The real service methods bypass that policy for reads while create correctly invokes and respects it. Required: the chosen read policy runs for external reads, or another proven layer supplies equivalent protection. Database Owner mode with distinct authenticated users can be an independent control; a gateway can also be one. This does not claim every plugin deployment exposes other users' tasks. It proves the plugin itself is not protecting those reads. Actual gateway/Gen3 integration was not exercised in this pass.

Reviewer: foreground.

## An additional authority-provenance concern remains open

A bounded initializer check produced:

```text
client_logs_retained=true client_backend_id_retained=true
```

Command: `go test ./tes -run '^TestAuditServerMetadataProvenance$' -v -count=1`.

`tes.InitTask` retains caller-supplied logs/metadata while initializing server fields. AWS/HPC backend lookup code reads provider IDs from those metadata keys. The run establishes persistence of untrusted data. It does not establish that a particular unauthorized provider operation can complete: provider IDs can be overwritten by submission, AWS cancellation is constrained by P07, and infrastructure permissions remain separate. This is an urgent provenance review item, not an additional demonstrated provider-control finding.

## What must be true before production approval

These are acceptance conditions for future remediation, not changes applied by this audit:

1. Tenant A's principal survives every database and policy adapter. The ordinary read/list/event interfaces cannot access or modify Tenant B's records. Positive owner/admin/system cases remain explicit.
2. Task data cannot alter runtime flags, Kubernetes object policy, worker identity, or trusted backend metadata. Execution specs are constructed from typed fields with policy checks at the external boundary.
3. Inputs use independent task-local storage where task writes are allowed. Output reads remain rooted to approved task data at the moment of opening, with defined symlink/hardlink/race behavior.
4. Executor code does not inherit worker/controller credentials. The deployed identity, effective token mount, RBAC, IAM, and network policy are tested together; rendered intent alone is insufficient.
5. Network authentication and encryption are effective on every exposed interface, including internal RPC paths. Missing request identity cannot silently grant administrative authority in an external request path.
6. Cancellation observes and initiates actual execution revocation. Provider failures remain visible after the application state transition, with retries and reconciliation verified.
7. Diagnostic output and CI artifacts do not copy credentials into broader-access locations. Authentication boot and token policy are tested, and the identified reachable toolchain advisory is removed from the deployed build.

## Evidence, dropped ideas, and limits

The previous inventory's confirmed findings remain relevant; this pass did not rerun its container-option or privileged-template probes. This pass used only bounded defensive checks. No production source was changed and no fixes were applied.

The platform reviewer completed. Its six explicit exclusions were: missing claim ownership (checks exist on inspected claim paths), missing gRPC authentication (the interceptor exists), Events over HTTP (Events is registered on gRPC only), fork-PR secret theft without a concrete path, branch-name injection without a concrete path, and optional broad Role permissions without a demonstrated unauthorized reader. Those exclusions do not cancel existing event-authorization findings.

API and storage reviewers were stopped by a cybersecurity safeguard before completing this pass. Their full dropped-finding counts are unavailable. Their partial messages and unfinished artifacts are not treated as verified findings. The foreground independently checked the narrow identity-retention, staging, and output-classification properties described above. No blocked database protocol or data-disclosure fixture was executed by the foreground.

A narrow tracked-file signature scan for private-key/access-token markers matched only `config/safe_test.go`, which is a synthetic redaction fixture. This is not a full secret scanner or historical secret audit. CI source review found no additional proven untrusted-PR-to-publishing-credential path. Docker availability was not established, and no live cluster/cloud/database checks ran.

Remaining high-priority evidence gaps include each cloud database's actual event-write authorization, real tenant isolation behind the intended Gen3/gateway configuration, provider/IAM enforcement, deployed Pod admission/RBAC/network policies, output publication with the chosen Linux/storage stack, concurrent state/race behavior, and real execution termination. A production organization should keep those gates open rather than interpret an incomplete test as a pass. This review is deeper than the initial inventory, but remains bounded and does not establish that all vulnerabilities have been found.
