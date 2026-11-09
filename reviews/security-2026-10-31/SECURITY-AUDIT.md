# Repository security issue inventory

The [production security gate review](production/PRODUCTION-SECURITY-REVIEW.md) supersedes this initial pass for prioritization and corrects its PostgreSQL read-isolation assumption. The original findings and evidence below are preserved.

Reviewed commit: `6c233af6e06e0a404dbc353310dab2c886de0b45` (`feature/scheduler-reliability-1027`). Review date: 2026-10-31.

The review found **19 locally reproduced security-relevant defects or control gaps**, and **one reachable dependency advisory**. Three additional dependency advisories match module versions without a demonstrated affected call path. These categories are deliberately separate. Severity is an engineering assessment conditional on the stated deployment, not a CVSS score or evidence that a deployed service has been compromised.

## Intent

Determine what is wrong with the security of the entire repository, including existing code outside the branch diff. Inventory failures, identify the boundary each breaks, and preserve commands and actual output. Do not apply fixes. Authorized execution of a submitted task is a feature; gaining worker, infrastructure, host, or another user's authority through task data is a separate boundary.

## Inventory

| ID | Severity and qualification | Observed failure |
| --- | --- | --- |
| S01 | High; potentially critical if daemon permits host privileges | Task fields become Docker options; GCP workdir becomes extra container options |
| S02 | High; potentially critical if admission permits privileged pods | Raw image interpolation changes Kubernetes Job security settings |
| S03 | High when restricted local storage is used | Local storage allowlist accepts sibling, traversal, and escaping symlink paths |
| S04 | Medium; bounded escape, task path knowledge required | FileMapper writes into a sibling outside the task workspace |
| S05 | High with reachable Events RPC and BoltDB | Another authenticated user changes task metadata and execution logs under Owner policy |
| S06 | High with Badger and reachable Events RPC | Raw create event replaces another user's task and ownership |
| S07 | High in deployments separating tenants from infrastructure | Ordinary authenticated user deletes scheduler nodes |
| S08 | High with local backend and per-user plugin credentials | One request's plugin response mutates shared backend credentials |
| S09 | High across an untrusted network; reduced by secure tunnels | HTTP and gRPC clients dispatch reusable credentials without requiring encryption |
| S10 | High with configured nonanonymous FTP credentials | Task-selected FTP endpoint receives global storage credentials |
| S11 | High when users expect SFTP confidentiality | `sftp://` sends ordinary plaintext FTP commands |
| S12 | Medium; authentication availability/configuration failure | Enabling OIDC reaches a nil pointer during initialization |
| S13 | Medium; latent behind S12 | OIDC callback state is reusable across browser sessions sharing a User-Agent |
| S14 | Medium; policy-dependent, latent behind S12 | OIDC defaults accept wrong-audience and no-expiry signed tokens |
| S15 | High when executor data contains secrets and logs have broader readers | Package debug logger emits task environment regardless application log level |
| S16 | Medium with MongoDB URI credentials and debug configuration logs | `Config.Safe()` retains a supported URI's password |
| S17 | Medium when CI captures these secret representations | Gen3 redactor preserves signed URLs, URL passwords, and YAML environment secrets |
| S18 | Medium when HTTP is exposed without upstream limits | Gateway buffers a large unauthenticated task body before authentication rejection |
| S19 | Medium for GCP Batch workloads requiring revocation | Cancellation succeeds without contacting the cloud backend |
| D01 | Reachable advisory; exploit conditions not reproduced | Go 1.27.2 includes affected certificate verification code; fixed in 1.27.3 |

## Reproduction and evidence

From the repository root:

```sh
bash reviews/security-2026-10-31/verify.sh
```

The script archives the reviewed commit to a fresh temporary directory, adds audit tests only there, and records outputs. It does not modify production code, launch containers, contact cloud services, or apply fixes. It uses cached Go dependencies and synthetic data. Passing checks mean that the recorded unsafe behavior was reproduced; they do not mean that security checks passed.

The FTP check needs a localhost listener and is opt-in:

```sh
bash reviews/security-2026-10-31/verify.sh --with-loopback
```

It sends fake credentials only to `127.0.0.1`. The foreground ran it separately twice and saved [its output](evidence/ftp.txt). The default complete script was also run successfully; its copied evidence is under [evidence](evidence/).

For the individual commands below, `cd` into the source snapshot printed by the script. `PROBES` refers to its adjacent `probes` directory. All `.go` probes are preserved as `.go.txt` under [probes](probes/) so normal repository builds do not compile them. `GOCACHE=/tmp/funnel-security-gocache` and `GOPROXY=off` were set for Go checks.

### S01. Container data becomes runtime options

Sources: `config/default.go:97-113`, `worker/docker.go:108-150`, `worker/docker.go:GetEnvArgs`, `tes/validate.go:66`, `compute/gcp_batch/backend.go:286`.

The Docker command is a text template followed by `shellwords.Split`. Image validation checks nonemptiness, and environment keys are not constrained. Task strings become runtime options outside the container. GCP Batch similarly concatenates `Workdir` into `Runnable.Container.Options`.

Commands:

```sh
go run "$PROBES/docker.go"
go test ./compute/gcp_batch -run '^TestAuditContainerOptions$' -v -count=1
```

Output excerpts:

```text
validation=
argv=["run" "-i" "--read-only" "--tmpfs" "/tmp" "--env" "X" "--privileged" "--volume" "/:/host" "--env" "Y=value" "--privileged" "--volume" "/:/host" "alpine"] error=<nil>
accepted_workdir="/work --privileged" serialized_container_options="--workdir /work --privileged"
```

Required: reject invalid image/environment/workdir values or preserve each as data without adding runtime options. The Docker probe mirrors the actual template and splitting code; the GCP check captures the actual `Submit` request. No Docker daemon or cloud job ran. Successful privilege escalation depends on daemon/cloud restrictions. Docker and GCP variants share the root cause of serializing untrusted data into an option string and are counted together. Reviewers: storage/execution and foreground. Evidence: [docker](evidence/docker.txt), [GCP check](evidence/unit-checks.txt).

### S02. Image data changes the Kubernetes security context

Sources: `config/kubernetes/executor-job.yaml:51`, `worker/kubernetes.go:172-202`, `tes/validate.go:66`.

Command: `go run "$PROBES/yaml.go"`.

```text
validation=
render_error=<nil>
decode_error=<nil>
decoded_image="alpine" privileged=true allowPrivilegeEscalation=true
```

A synthetic multiline image passes TES validation and the bundled executor template turns it into actual `securityContext` fields when decoded as a Job. Required: reject the image or keep it as a scalar, without changing security policy. Preconditions: a deployment uses the bundled template or a derivative with raw interpolation, and admission allows the resulting fields. The default `ExecutorTemplate` is empty; this is not a claim about every Kubernetes deployment. No API request or privileged pod ran. Reviewers: storage/execution and foreground. Evidence: [YAML probe](evidence/yaml.txt).

### S03. Local storage does not confine access to allowed directories

Sources: `storage/local.go:107-129`, `storage/mux.go:findBackend`.

Command: `go run "$PROBES/local_containment.go"`.

```text
case=allowed-sibling/marker allowed=true copied="synthetic-outside-marker" error=<nil>
case=allowed/../allowed-sibling/marker allowed=true copied="synthetic-outside-marker" error=<nil>
case=allowed/link/marker allowed=true copied="synthetic-outside-marker" error=<nil>
```

Required: all three outside-root requests are denied without copying data. This uses the real storage Mux and temporary synthetic files. Preconditions: restricted `AllowedDirs`, caller controls a storage URL, and worker OS permissions allow the outside file. Sibling and lexical traversal cases require no symlink control; the symlink case requires an escaping link. Prefix matching is not filesystem containment. Reviewer: foreground. Evidence: [containment](evidence/local_containment.txt).

### S04. Task workspace mapping accepts prefix-sharing siblings

Sources: `worker/file_mapper.go:291-296`, `worker/file_mapper.go:404`, `worker/file_mapper.go:464-465`.

Command: `go run "$PROBES/mapper.go"`.

```text
validation=
map_error=<nil>
sibling_contents="synthetic attacker write" error=<nil>
```

An inline input at `/../taskA-sibling/secret` maps outside `taskA`, and `MapTask` creates/writes the sibling file. Required: reject the path and create no outside file. This is independent of local-storage URL routing. Preconditions include knowledge of the task workspace basename and permission to write its parent. The demonstrated escape is limited to prefix-sharing paths; arbitrary unrelated host paths or access to another randomly named task were not demonstrated. Reviewers: storage/execution and foreground. Evidence: [workspace write](evidence/mapper.txt).

### S05. Owner policy does not protect all task event writes

Sources: `events/service.go:25`, `database/boltdb/events.go:65-170`, `server/server.go:156-159`.

Command:

```sh
go test -v "$PROBES/api_audit_test.go" -run 'TestGRPCCrossOwnerEvent|TestCrossOwnerEventsAndNodes' -count=1
```

```text
bob read=permission denied state=permission denied metadata=<nil> stdout=<nil> exit=<nil>; alice metadata=map[audit:bob-controlled] stdout="bob-controlled-output" exit=99
 authenticated bob GRPC WriteEvent=<nil>; alice read=<nil> metadata=map[audit:bob-via-grpc]
```

The second line is from the generated gRPC client/server over an in-memory connection with the real authentication interceptor. Required: Bob's event writes return permission denied and Alice's records remain unchanged. Preconditions: BoltDB, authenticated account, reachable Events RPC, and known task ID. Timestamp, output, stderr, and system-log branches show the same missing owner guard by inspection; only the demonstrated metadata/stdout/exit-code paths are claimed as runtime verified. State mutation and reads correctly deny Bob. Other database variants are listed separately below. Reviewers: API and foreground. Evidence: [API checks](evidence/api-checks.txt).

### S06. Badger raw task creation replaces existing ownership

Sources: `database/badger/events.go:44-57`, `events/service.go:25`; validation bypass compared with `server/tes.go:149` and `tes/utils.go:95`.

Command: `go test -v "$PROBES/api_audit_test.go" -run '^TestBadgerRawCreateTakesOwnership$' -count=1`.

```text
bob read before=permission denied overwrite=<nil> read after=<nil> name=bob-overwrite state=COMPLETE; alice read after=permission denied
```

Required: reject replacement of an existing task and preserve its data/owner. Preconditions: Badger, authenticated account, reachable Events RPC, and known existing task ID. The test invokes the real Events service with identities produced by the authentication interceptor. It proves replacement and ownership takeover, not retrieval of the victim's original contents. BoltDB duplicate creation is guarded. Reviewers: API and foreground. Evidence: [API checks](evidence/api-checks.txt).

### S07. Authentication does not separate scheduler administration

Sources: `server/server.go:275-280`, `database/boltdb/scheduler.go:60-124`, `cmd/server/run.go:305-306`.

Command: `go test -v "$PROBES/api_audit_test.go" -run '^TestCrossOwnerEventsAndNodes$' -count=1`.

```text
bob delete node=<nil>; alice subsequent get=rpc error: code = NotFound desc = not found: nodeID: victim-node
```

An ordinary authenticated user deletes a node created under a different identity. Required for a tenant/infrastructure trust boundary: only authorized node identities or administrators can modify infrastructure. Preconditions: scheduler enabled and normal user accounts can reach these APIs. There is no implemented node-owner policy to bypass; this is missing infrastructure role separation. Source registers the service through both HTTP gateway and gRPC. The test proves deletion, not a node-spoofing-to-code-execution chain. Reviewers: API and foreground. Evidence: [API checks](evidence/api-checks.txt).

### S08. Per-request plugin credentials contaminate shared configuration

Sources: `compute/local/backend.go:52-88`.

Command: `go test ./compute/local -run '^TestAuditPluginConfigIsolation$' -v -count=1`.

```text
shared configuration user after per-request plugin response=alice
```

Required: the shared deployment credential stays `deployment`; the request-specific plugin response applies only to that request. The test calls the actual backend `WriteEvent` with a synthetic plugin response. Preconditions: local backend, plugin enabled, plugin returns per-user configuration. No malicious plugin is required; legitimate per-user credentials are merged into the shared object. A simultaneous cross-request credential use or race was not tested. Reviewer: foreground. Evidence: [unit checks](evidence/unit-checks.txt).

### S09. Clients dispatch reusable credentials without requiring encrypted transport

Sources: `tes/client.go:NewClient/GetTask`, `util/rpc/rpc.go:39-52`. Server gRPC is also created without TLS at `server/server.go:151`.

Commands:

```sh
go run "$PROBES/credential_transport.go"
go test ./util/rpc -run '^TestAuditRPCPlaintextCredentials$' -v -count=1
```

```text
scheme=http credentials=true user=synthetic-user password=synthetic-secret
error=<nil>
credentials_received=true transport_auth=<nil>
```

Required for secure defaults: remote reusable credentials require secure transport or an explicit administrator override. The HTTP recorder observes the actual client's request; gRPC uses the real `rpc.Dial` over an in-memory transport. Preconditions: credential-bearing HTTP/RPC leaves a trusted local boundary without an independently secured tunnel. No external interception occurred. HTTP and RPC are grouped under one transport-policy issue. Reviewers: API and foreground. Evidence: [HTTP](evidence/credential_transport.txt), [RPC](evidence/unit-checks.txt).

### S10. Configured FTP credentials are not bound to a trusted host

Sources: `storage/ftp.go:95-130`, `config/config.proto:FTPStorage`.

Command: `go run "$PROBES/ftp.go"` (requires localhost binding).

```text
scheme=ftp plaintext_commands=["USER synthetic-config-user" "PASS synthetic-config-password"] operation_error=ftpStorage: logging in: 530 "synthetic fixture rejects login"
```

The task URL contains no userinfo; configured global credentials reach the selected local test server before the fixture rejects authentication. Required: only send configured credentials to their configured trusted host. Preconditions: FTP storage enabled with nonanonymous global credentials, task-controlled FTP destination, and permitted route to that destination. The config has no host restriction. This proves credential delivery with synthetic values, not theft of real credentials. Reviewers: storage/execution identified; foreground reproduced twice. Evidence: [FTP](evidence/ftp.txt).

### S11. `sftp://` uses plaintext FTP rather than SSH

Sources: `storage/ftp.go:82-110`.

Command: `go run "$PROBES/ftp.go"`.

```text
scheme=sftp plaintext_commands=["USER synthetic-config-user" "PASS synthetic-config-password"] operation_error=ftpStorage: logging in: 530 "synthetic fixture rejects login"
```

Required: use SSH/SFTP or reject the unsupported scheme before sending credentials. Explicit test ports keep both schemes on the synthetic localhost fixture. The implementation changes the default port to 22 but still uses `ftp.Dial`; there is no SSH handshake. Even URL-specific credentials are exposed if a purported SFTP endpoint speaks FTP. This is independent of S10's host-scoping defect. Reviewers: storage/execution identified; foreground reproduced twice. Evidence: [FTP](evidence/ftp.txt).

### S12. OIDC configuration crashes initialization

Sources: `server/auth_oidc.go:67-79`.

Command: `go test ./server -run '^TestAuditOIDCStartup$' -v -count=1`.

```text
configured OIDC initialization panicked: runtime error: invalid memory address or nil pointer dereference
```

The test supplies synthetic successful discovery/JWKS responses without a network request. `initConfig` assigns `c.oauth2.ClientID` without allocating `c.oauth2`. Required: initialize authentication without a panic. This is an availability and secure-configuration blocker, not proof that a crashing deployment silently becomes unauthenticated. Reviewer: foreground. Evidence: [OIDC checks](evidence/unit-checks.txt).

### S13. OIDC login state is not bound to a browser session

Sources: `server/auth_oidc.go:116-160`, `server/auth_oidc.go:190-200`.

Command: `go test ./server -run '^TestAuditOIDCLoginBinding$' -v -count=1`.

```text
fresh session cookies=0 callback status=307 sets-session-cookie=true
different user agent callback status=400
```

A state obtained through one request is accepted in a fresh session with the same User-Agent, with a synthetic token exchange establishing its session cookie. Required: state is unpredictable per login, tied to that browser session, and single use. Current state hashes stable configuration and User-Agent and truncates to ten hex digits. It is not browser-specific even though the client secret itself is not disclosed. This demonstrates a handler-level login-CSRF prerequisite, not a live identity-provider attack. **Latent:** S12 blocks normal OIDC startup on this commit. Reviewer: foreground. Evidence: [OIDC checks](evidence/unit-checks.txt).

### S14. Default OIDC token policy permits other audiences and missing expiry

Sources: `server/auth_oidc.go:213-260`, `config/config.proto:OidcAuth`.

Command: `go test ./server -run '^TestAuditOIDCTokenPolicy$' -v -count=1`.

```text
wrong audience accepted=true
missing expiry accepted=true
expired control accepted=false
wrong audience with explicit policy accepted=false
```

Tokens are signed with a synthetic trusted issuer key and checked through the actual JWKS/parser code. Required for a deployment expecting audience-bound, expiring access tokens: require the intended audience and an expiry claim. Preconditions: trusted issuer issues these token shapes, `RequireAudience` is unset for the wrong-audience case, and no introspection endpoint independently rejects them. Explicit audience policy works; expired tokens are rejected. This is a configurable/default-policy gap, not acceptance of a forged signature. Audience and expiry are grouped as one incomplete token-profile policy. **Latent:** S12 blocks normal OIDC startup. Reviewer: foreground. Evidence: [OIDC checks](evidence/unit-checks.txt).

### S15. Global debug logging exposes executor environment despite normal log levels

Sources: `logger/debug.go:3-19`, `worker/kubernetes.go:156-191`, `worker/worker.go:393-398`; GCP also uses this global logger for job requests at `compute/gcp_batch/backend.go:335`.

Command: `go run "$PROBES/logger.go"` (capture stdout/stderr separately).

```text
application_debug_suppressed=true
```

The captured stderr contains `PASSWORD` and `synthetic-secret` in the supplied environment map. Required: normal application log level governs diagnostic output, and task credentials are not copied into these logs. The runtime check proves the actual global logger ignores the application logger's level and serializes synthetic secret data. Source connects this logger to full executor template data and rendered Job YAML; a live Kubernetes worker was not run. Preconditions: task secrets in environment/arguments and log readers with broader access than task credentials. Reviewers: UI/supply-chain and foreground. Evidence: [stdout](evidence/logger.stdout), [stderr](evidence/logger.stderr).

### S16. `Config.Safe()` leaves supported MongoDB URI credentials readable

Sources: `config/safe.go:14-15`, `database/mongodb/new.go:30-31`; actual debug sinks at `cmd/server/run.go:57` and `cmd/worker/run.go:35`.

Command: `go run "$PROBES/config_safe.go"`.

```text
mongo_uri_password_retained=true
separate_password_redacted=true
```

Required: URI userinfo is also redacted in the safe copy. MongoDB accepts a URI in `Addrs[0]`; redaction covers only the separate `Password`. Preconditions: credentials supplied through URI and debug configuration logging enabled. The check uses synthetic values and does not inspect real configuration. Reviewers: UI/supply-chain and foreground. Evidence: [safe configuration](evidence/config_safe.txt).

### S17. CI artifact redaction misses common credential representations

Sources: `tests/gen3/redact_logs.py:9-28`, `tests/gen3/run-command.py:18-23`, `.github/workflows/integration_tests_on_kind.yaml:209-218`.

Command: `python3 <review-directory>/probes/redaction_probe.py` from the source snapshot.

```text
named: changed=True; output='password=[REDACTED]'
presign: changed=False; output='https://example.invalid/object?X-Amz-Credential=synthetic-id&X-Amz-Signature=synthetic-signature'
envyaml: changed=False; output='- name: PASSWORD\n  value: synthetic-secret'
userinfo: changed=False; output='ftp://user:synthetic-secret@example.invalid/file'
```

Required for an artifact represented as redacted: remove these secret values before upload. Preconditions: such values occur in collected output. Runtime verification calls the real redactor with synthetic fixtures; actual CI artifact contents were not inspected. This establishes redactor coverage gaps, not evidence that existing artifacts contain real secrets. Reviewers: UI/supply-chain and foreground. Evidence: [redaction](evidence/redaction.txt).

### S18. Gateway fully buffers a large request before authentication rejection

Sources: `server/marshal.go:196-213`, `tes/tes.pb.gw.go:500-510`, `server/server.go:283-286`.

Command: `go test ./server -run '^TestAuditGatewayBuffersBeforeAuth$' -v -count=1`.

```text
authorization_header="" fully_decoded_description_bytes=5242880 response_status=401
```

The actual generated HTTP gateway and custom marshaler decode a 5 MiB description without an Authorization header before a recording RPC client returns unauthenticated. The decoder uses unrestricted `io.ReadAll`, and the server supplies no request-body cap or HTTP read timeouts. Required: enforce an explicit request budget before full buffering or reject unauthorized requests earlier. The check proves processing order and buffering, not an actual out-of-memory attack or an arbitrary required size threshold. Preconditions: HTTP endpoint exposed without compensating proxy limits. Reviewer: foreground. Evidence: [gateway check](evidence/unit-checks.txt).

### S19. GCP Batch cancellation reports success without revoking execution

Sources: `compute/gcp_batch/backend.go:369-372`, `server/tes.go:293-305`, `compute/gcp_batch/client.go`.

Command: `go test ./compute/gcp_batch -run '^TestAuditCancelNoClient$' -v -count=1`.

```text
cancel with no cloud client returned=<nil>
```

The actual compute backend event path accepts cancellation even with no cloud client. `Cancel` is an unconditional no-op. The TES cancellation path marks the task canceled before dispatching backend cleanup. Required: cancellation initiates cloud-job termination or reports that revocation was not performed. Preconditions: GCP Batch direct execution and a running job whose continued execution matters. The local test proves no revocation call is made; continuing work/cost on a real cloud job was not measured. Reviewer: foreground. Evidence: [GCP check](evidence/unit-checks.txt).

## Dependency checks

Fresh advisory queries were performed. Offline npm's earlier empty-cache zero result was discarded; the result below is the subsequent online query.

Commands:

```sh
GOCACHE=/tmp/funnel-security-gocache /tmp/funnel-govulncheck-current -json ./...
npm audit --json   # in webdash
```

The initially installed `govulncheck` v1.3.0 could not parse this Go 1.27 source. A temporary scanner was built from cached `golang.org/x/vuln@v1.9.0` using Go 1.27.2. Its build version displays `v0.0.0`; the source version is recorded here. The database identifies its last modification as `2026-10-27T21:16:18Z`. The foreground inspected the output and the four corresponding primary advisory pages. Results: [filtered scanner output, including all six findings and the four associated advisory records](evidence/dependency-findings.json), [npm output](evidence/npm-audit.json).

### D01. Reachable certificate-verification advisory affects the pinned toolchain

`go.mod:3` specifies Go 1.27.2. The scanner reports [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809), fixed in Go 1.27.3, with a symbol trace reaching `crypto/x509.(*Certificate).Verify` through TLS/HTTP and `util/fsutil/io.go:36`.

The advisory concerns distrust of compromised intermediate certificates in cases where system trust updates alone do not cover verification. The run verifies the affected toolchain version and a reachable symbol, not a successful malicious-certificate connection. Required dependency state: a toolchain with the applicable fix. This is one advisory, not three vulnerabilities: module-, package-, and symbol-level records share its ID.

### Module-only matches, not demonstrated application vulnerabilities

| Advisory | Selected module | Finding depth | Boundary of this result |
| --- | --- | --- | --- |
| [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) | `golang.org/x/crypto v0.58.0` | Module only | Advisory applies to unmaintained `openpgp`; no affected package/function path was reported |
| [GO-2026-6860](https://pkg.go.dev/vuln/GO-2026-6860) | `golang.org/x/net v0.61.0` | Module only | HTML parser complexity issue; no affected application call path reported; fixed in v0.62.0 |
| [GO-2026-6866](https://pkg.go.dev/vuln/GO-2026-6866) | `golang.org/x/net v0.61.0` | Module only | HTML rendering namespace issue; no affected application call path reported; fixed in v0.62.0 |

These remain dependency hygiene observations, excluded from the 19 reproduced local findings and the one reachable advisory. The 303 advisory records downloaded by the scanner are database lookup results, not 303 vulnerabilities in this repository.

Online npm audit reports zero known advisories across the 217 dependencies counted in its output. This is a dated advisory result, not proof that dashboard code or dependencies have no defects.

## Source concerns requiring additional verification

These are not included in the confirmed-finding count. They are retained so the inventory does not hide uncovered risk.

| Area | Source observation | Missing evidence or condition |
| --- | --- | --- |
| Other database event writes | MongoDB/Postgres task-log updates and DynamoDB/Datastore stdout or system-log writes show missing owner predicates in inspected paths | Runtime verification against each backend; S05 is verified only on BoltDB |
| Other raw-create paths | DynamoDB unconditional PutItem and Datastore PutMulti resemble the Badger overwrite path | Backend-specific replacement and ownership tests |
| Default network exposure | `config/default.go:26-33` sets no authentication and `TaskAccess: All`; `cmd/server/run.go:290` binds RPC with `":"+port`; `server/server.go:284` binds HTTP similarly | Deployed firewall, proxy, and launch configuration not inspected; `HostName: localhost` is an advertised address, not a bind restriction |
| HTTP/storage egress | `storage/http.go` accepts arbitrary HTTP(S) hosts and follows allowed redirects; no application-level private-address policy was found | Establish an intended egress restriction and deployed network reachability before claiming SSRF impact |
| HTTP token endpoint and reverse proxy | `server/auth_oidc.go:173` echoes JWT without an explicit no-store response header; cookie Secure depends on backend `req.TLS`, not forwarded TLS state | Browser/proxy caching and actual TLS termination behavior; latent behind S12 |
| HTTP/OIDC response budgets | OIDC discovery/introspection use unbounded response reads and some outbound clients lack timeouts | Bounded remote-response test and realistic controlling-party model; no exhaustion run was performed |
| Release tooling | `Makefile:203` installs GoReleaser `@latest` in release workflow with write credentials | Mutable tool selection is verified by source; no compromised dependency or release run was demonstrated |
| Integration installers | Workflows download and execute remote installers without fixed content | Supply-chain trust risk; no compromise demonstrated |
| Installation script | `install.sh` extracts into current directory and uses unquoted destination expansions | A bounded installation/path test; it does verify SHA-256 before extraction |
| Kubernetes RBAC | Bundled `config/kubernetes/role.yaml` grants namespace-wide secret/configmap reads and broad job/pod/PVC permissions | A deployment using this role, actual identity bindings, and denied/allowed operation tests |
| Worker configuration distribution | Per-task ConfigMap can contain the full configuration | An unauthorized reader must be established; configuration available to a trusted worker is intentional |
| GCE-COS examples | Publish HTTP/RPC ports, mount Docker socket, use mutable image tag | Actual launch/firewall state; sample firewall source-tags do not establish internet-wide access |
| Plugin binary discovery | `plugins/shared/manager.go` discovers siblings and chooses a binary from an administrator-selected directory | Attacker write access to that trusted directory; no remote entry point established |

## Dropped findings and controls that worked

All three reviewers in this pass completed: `gpt-6.1-sol` at low effort, with distinct API, storage/execution, and UI/supply-chain briefs. All surviving findings were rerun by the foreground. Cross-judging and fix/graft phases were skipped as required by interrogate. The previous turn's reviewer safety failures are not counted as successful reviews; this pass has its own completed evidence.

The following counts describe explicit review exclusions, not an estimate of all hypotheses considered:

- **API reviewer:** one alleged issue disproved: DynamoDB `WithFilter` does generate the owner filter. Six backend-specific variants were withheld from the independent finding count: event-write concerns in MongoDB/Postgres/DynamoDB/Datastore and raw-create concerns in DynamoDB/Datastore. A mocked response ignoring its own filter is not evidence of a real authorization bypass.
- **Storage/execution reviewer:** three candidate concerns excluded as independent vulnerabilities: full worker configuration distribution without an unauthorized reader, HPC config permissions (actual files use `0600`), and plugin-directory selection without an attacker write path. FTP concerns initially lacked runtime evidence and were subsequently verified by the foreground as S10/S11.
- **UI/supply-chain reviewer:** three exploit claims withheld: browser HTML injection without a raw-HTML sink, token exfiltration through URL validation without a controllable caller, and a Postgres URI redaction bypass without a supported URI input. Offline npm zero output was also discarded as advisory evidence. Source-only deployment/release concerns remain above rather than being promoted into reproduced vulnerabilities.

Controls observed working:

- BoltDB Owner policy rejects cross-user reads/state mutation; execution claims deny Bob and admit Alice. The runtime output is preserved in [API checks](evidence/api-checks.txt).
- DynamoDB query generation includes its owner filter; its apparent discarded builder result was misleading because the builder shares internal state.
- Inspected database read/list paths contain owner checks/filtering, but the follow-up P01 found PostgreSQL drops the caller identity before its checks. Presence of a check is not evidence that isolation works; cloud-backend behavior remains incompletely exercised.
- Explicit OIDC audience policy and expired-token rejection work in synthetic checks. Existing signature/issuer tests were inspected; they were not rerun here because they use socket listeners.
- Mounted-S3 code uses rooted file access, validates mount identity/path structure, rejects nonregular write targets, and stages atomic publication. This pass inspected these controls rather than claiming a fresh exhaustive race test.
- Dashboard source uses React text rendering, fixed-origin API construction, and in-memory bearer token storage; no raw HTML sink was found. No browser session was driven.
- GitHub Actions are SHA pinned, frontend installation uses the lockfile, and frontend CI checks embedded asset drift.

## Coverage and limits

The pass inspected entry points, authentication, all database families' access checks, scheduler/events, local/Docker/Kubernetes execution, storage protocols, mounted S3, plugins, AWS/GCP/HPC compute surfaces, dashboard code, config redaction, CI/release/install paths, and deployment examples. Runtime checks concentrate on reproduced boundaries, not every method of every backend.

No claim of mathematical exhaustiveness is possible from this review. Open verification includes live PostgreSQL/MongoDB/DynamoDB/Datastore behavior, cloud IAM and deployed RBAC/admission, hostile-container behavior, scheduler spoofing consequences, concurrent configuration races, actual proxy/caching/firewall behavior, dependency paths omitted by this host's build tags, production binaries/toolchain provenance, and real artifact contents. Secrets and live systems were not inspected or modified. FTP probes used only synthetic localhost services; containers and cloud jobs were not launched.

The existing security-hardening worktree was preserved. Its draft changes were not used as the implementation under test. No production fixes, commits, pushes, or external issue reports were made.
