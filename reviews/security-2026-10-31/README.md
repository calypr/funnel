Current remediation status: [all 32 backlog outcomes](REMEDIATION.md), with fixes, deferred designs, source-only evidence gaps, and a working-tree [verification script](remediation/verify.sh). The audit reports below describe the frozen reviewed revision; their defect-reproduction scripts are not tests of the fixes.

Latest expanded audit pass: [source and configuration review](expanded/EXPANDED-REVIEW.md), with P11/P12, explicit source concerns, and a bounded rerun script.

Latest pass: [cleanup ownership, upload sources, and policy coverage](followup/FOLLOWUP-REVIEW.md).

Current assessment: [production security gate review](production/PRODUCTION-SECURITY-REVIEW.md), with bounded defensive checks and explicit release blockers.

The broader follow-up is now available in [the repository security inventory](SECURITY-AUDIT.md), with 19 reproduced findings, dependency checks, and a rerun script. The checkpoint below is preserved as the earlier state.

# Security review checkpoint — 2026-10-31

## Intent

Review the entire repository for security failures, establish what work is already committed, and compare competing security architectures before choosing fixes. Preserve existing work. Report only reproduced failures as findings; do not apply fixes during interrogation.

## Branch state

Reviewed HEAD: `6c233af6`, branch `feature/scheduler-reliability-1027`.
Using local remote references (not freshly fetched): 28 commits ahead of `origin/develop`; one ahead of its upstream branch. The recent commits concern scheduler correctness, recovery, pagination, tests, and CI.

`feature/security-hardening` is checked out in `/private/tmp/funnel-security-hardening`. Its commit `f4fd9bae` is an ancestor of reviewed HEAD, 68 commits behind, with no unique commits. It has uncommitted changes concerning event authorization, scheduler access, credential transport, local filesystem containment, and per-request backend configuration. These are draft changes, not fixes present on reviewed HEAD. They were inspected but not modified or validated by this review.

The primary worktree initially had untracked `error.log`, `funnel`, `reviews/`, and `webdash/test-results/`. No tracked production files were changed during this review. This directory contains new review artifacts; no commits were created.

## Verified findings

### 1. Local-storage allowlist does not enforce filesystem containment

Source: `storage/local.go:107-129`; `storage/mux.go:Get` selects the backend through this policy.

Preconditions: local storage enabled with a restricted `AllowedDirs`; caller can supply a storage URL; worker OS identity can access the outside file. The sibling-prefix and traversal cases need no symlink control. The symlink case requires an escaping link in the allowed tree.

Run from repository root:

```sh
cp reviews/security-2026-10-31/local_containment_probe.go.txt /tmp/funnel-local-containment-probe.go
GOCACHE=/tmp/funnel-security-gocache go run /tmp/funnel-local-containment-probe.go
```

Actual output, reproduced twice using the identical probe in `/tmp/funnel-security-containment/main.go`:

```text
case=allowed-sibling/marker allowed=true copied="synthetic-outside-marker" error=<nil>
case=allowed/../allowed-sibling/marker allowed=true copied="synthetic-outside-marker" error=<nil>
case=allowed/link/marker allowed=true copied="synthetic-outside-marker" error=<nil>
```

Required: all three paths rejected and no copied data. Prefix checks accept a sibling and lexical traversal; filesystem operations follow the escaping symlink. The probe uses the actual storage Mux and only synthetic temporary files. This proves an allowlist boundary failure, not a full deployed task-to-exfiltration chain. Found and rerun by the foreground reviewer.

### 2. TES client accepts Basic credentials over remote HTTP

Source: `tes/client.go:NewClient` and `GetTask` (`SetBasicAuth` without a transport requirement).

Preconditions: client configured with an HTTP remote server and credentials, and the underlying connection lacks an independently secured tunnel. An observer on that transport can read the Basic credentials.

```sh
cp reviews/security-2026-10-31/credential_transport_probe.go.txt /tmp/funnel-credential-transport-probe.go
GOCACHE=/tmp/funnel-security-gocache go run /tmp/funnel-credential-transport-probe.go
```

Actual output, reproduced twice using the identical probe in `/tmp/funnel-security-transport/main.go`:

```text
scheme=http credentials=true user=synthetic-user password=synthetic-secret
error=<nil>
```

Required for a secure-by-default client policy: reject remote credential-bearing HTTP before dispatch unless an administrator explicitly permits it. The recording transport receives the real client's request and performs no network I/O. This proves dispatch without a transport guard; it is not a packet-capture demonstration. Found and rerun by the foreground reviewer.

## Compare security designs before choosing fixes

These are competing designs to prototype, not implemented or benchmarked alternatives. No design has yet passed an end-to-end comparison.

| Design | Boundary | What today's probes show | Remaining experiment |
| --- | --- | --- | --- |
| Harden the shared service | Central authorization for each RPC; credential transport policy; filesystem operations confined to configured roots | Existing prefix checks and permissive transport defaults fail | Run the same positive/negative tests across every RPC and storage operation, including concurrent symlink replacement |
| Task-scoped worker capabilities | Worker authority limited to one task and approved input/output objects; control-plane methods require separate authority | Current URL allowlist alone cannot provide this isolation | Prototype task A worker credentials against task B events, scheduler operations, and undeclared storage objects |
| Tenant-isolated execution | Separate worker identities, filesystem roots, storage credentials, and constrained network egress | Application path checks cannot substitute for OS and identity boundaries | Two-tenant deployment probe showing denied cross-tenant file, object, event, and network access while normal jobs still complete |

The immediate decision is how much tenant and worker compromise the product must contain. Hardened shared-service checks are necessary, but should not be treated as sufficient evidence of hostile multi-tenant isolation. Capability restrictions and tenant isolation need concrete prototypes before adopting an architecture.

## Independent-review results and dropped hypotheses

The configured interrogate panel has three `gpt-6.1-sol` reviewers at low effort. Identity/API, worker/execution, and storage reviewers were launched separately with no-write briefs and isolated temporary output paths. All three ultimately failed with tool safety flags; none returned a completed verified finding set. They are dropouts, not clean reviews. Per-reviewer dropped-finding counts are unavailable, not zero.

An identity reviewer prepared hypotheses involving EventService ownership, scheduler permissions, OIDC audience/expiry checks, and browser login state. No result from those hypotheses was accepted here because the foreground did not rerun and verify them. The security worktree's backend configuration isolation change is likewise unverified.

The initial HTTP listener probe was blocked by the sandbox's port-binding restriction. It was replaced by the recording transport above. Go's default build-cache path was not writable; subsequent commands used a temporary cache.

## Coverage still open

This checkpoint is not a completed repository-wide security audit. Open areas include per-method authorization and event mutation, OIDC and login flows, plugin configuration and secret isolation, executor and Kubernetes confinement, HTTP redirect/egress policy, non-local storage credentials, database filtering, dashboard/browser behavior, dependencies, CI supply chain, and deployment defaults. No live advisory database or remote branch state was queried, so this report makes no current dependency-vulnerability or remote-CI claims.
