Run the CI lifecycle locally on native ARM64 Linux with Docker on an ARM host:

```sh
bash tests/gen3/docker-dev.sh build-runner   # Once: pinned Ubuntu 24.04 ARM64 tools
bash tests/gen3/docker-dev.sh deps           # Once: pinned checkouts and Python dependencies
bash tests/gen3/docker-dev.sh build          # Once: create the owned cluster
bash tests/gen3/docker-dev.sh step images    # Once: cache the full pinned service images
bash tests/gen3/docker-dev.sh ci             # Fresh owned fixture; full CI test selection
```

Use `RUNNING_LOCAL=true bash tests/gen3/docker-dev.sh test -k <expression>`
after authentication has been initialized to reuse the saved test credentials.
The complete `ci` and `resume` runs always initialize authentication again.

Full OCI archives of the pinned Gen3 and CSI images are cached separately from
container state. A fresh cluster restores Postgres, MinIO, and CSI first. Helm/database setup
then overlaps restoration of the remaining application images; authentication
starts once Arborist, Fence, and IndexD images are ready, while gateway image
restoration continues. Deployment waits for all images to finish. Databases, users, and pods are created
from scratch. GitHub restores
the same image cache, keyed by architecture and fixture pins. A cache miss uses
the original registries and saves the complete images after setup succeeds.
Native helper and task-image archives are cached separately by architecture and
recipe content, avoiding repeated builds and exports. Imported images must match
both their architecture and recipe label.

The GitHub job uses `ubuntu-24.04-arm`. The local runner uses four CPUs,
16 GiB of memory, the same lifecycle script, four pytest workers, and the same
service image digests. Docker Desktop supplies the local Linux kernel, so host
kernels and networking still differ. Executable ELF headers are checked as well as image metadata, so Docker
Desktop binary translation cannot hide an x86 executable in an ARM image.

After a failure, use `docker-dev.sh resume` to rerun the lifecycle against the
retained fixture, or `docker-dev.sh step <phase>` to retry one bootstrap phase.
Use `docker-dev.sh test -k <expression>` for a focused test. These commands copy
and verify the current working files into a Linux volume; they do not read
mutable source through a shared mount. Dependency caches and cluster state
survive individual commands and `stop-runner`. Use `down` to delete only this
runner's owned Kind cluster. Reserve at least 15 GiB in Docker's virtual disk
before the first run.

For local debugging, keep the environment between edits. `dev.sh` stores its
kubeconfig, dependency caches, and per-command logs in ignored `.gen3-local/`.
It only accesses the cluster recorded as owned by this checkout. Local runs use
the fixture’s MinIO credentials and ignore host AWS profiles.

Prerequisites: Docker, Kind 0.31.0, kubectl, Helm 4.3.1, yq 4, jq, AWS CLI,
Python 3.13, uv, and GNU `timeout`. Put pinned tools in `.gen3-local/bin/`
or on `PATH`. Leave room in Docker’s virtual disk for service and executor
images. Local test and bootstrap steps require at least 2 GiB free there; host
filesystem free space alone does not establish Docker capacity.

```sh
bash tests/gen3/dev.sh deps                  # Once: pinned test dependencies
bash tests/gen3/dev.sh build                 # Once, then only for image changes
bash tests/gen3/dev.sh step tools            # Once: native helper and executor images
bash tests/gen3/dev.sh step prepare          # Once: database, auth, storage
bash tests/gen3/dev.sh step deploy
bash tests/gen3/dev.sh test -k directory      # Rerun a focused test
```

After a bootstrap failure, edit the script and rerun the failing phase with
`step database`, `step auth`, `step users`, or `step client`. These commands do
not rebuild images or recreate Kind. `step values` checks configuration without
deploying it. `status` and `logs deployment/fence-deployment` inspect retained
resources. Focused commands preserve the fixture; `ci` resets it, and `down`
deletes it. Focused tests start and stop their own HTTP port-forward when needed.

After a normal test run has initialized authentication fixtures, use
`RUNNING_LOCAL=true bash tests/gen3/dev.sh test -k directory` for focused
retries that reuse those fixtures. Run the complete selection without
`RUNNING_LOCAL` for final verification.

Local image builds include uncommitted source and use content-derived image tags.
Local and hosted Gen3 runs build ARM64 images. Focused local passes are
debugging evidence; require the complete fresh lifecycle for final verification.

The Gen3 check has no repository-imposed overall deadline. Its five-to-six-minute
runtime target still includes setup, tests, and diagnostics. GitHub's platform
limits still apply. Local and hosted runs use `FUNNEL_CI_DEADLINE_EPOCH=0` and
`GEN3_TEST_MAX_SECONDS=0` to disable lifecycle and pytest session deadlines.
Individual operations retain timeouts, real failures stop the run, and cancellation
still cleans up child processes. An explicit nonzero deadline remains available
for bounded debugging runs.

The repository owns the workflow and lifecycle runner. The runner uses pinned
integration-test and GitOps revisions, prepares dependencies alongside the
candidate build and two-node Kind cluster. Once Kind is ready, database/auth
bootstrap and storage setup run concurrently with compilation. The runner uses
bounded parallel image pulls and runs the upstream
`gen3_workflow and not wip` test selection. The fixture uses one replica of each
required Gen3 service and omits the unused Elasticsearch pod. Java 21 and
Nextflow 26.10.1 are checked before pytest. The original DICOM utility scripts
and requirements are fetched from checksum-verified image layers and built
for ARM64; the canary fixture uses native Ubuntu 24.04. The Gen3 AWS helper
retains its upstream scripts with a native AWS CLI, checked during its build.
Arborist is rebuilt from the exact source embedded in its pinned upstream image
because its ARM manifest ships an x86 server binary. Native helper and executor
binaries are checked before use; service executables are checked before pytest. These replace the two
AMD64-only upstream test images without changing the workflow assertions. The fixture retains real authentication services, PostgreSQL, MinIO, and Mountpoint CSI. Two
gateways share the same Funnel backend: `/workflows/` keeps the upstream Kind
bearer-token mode, while `/ga4gh/tes/` validates real DPoP proofs. This keeps the
task-token executor test enabled without changing ordinary client authentication.
Before pytest starts, a request with an invalid proof must return HTTP 401 with
`invalid_dpop_proof`; a disabled or misrouted gateway fails setup immediately.
Use `step dpop` to refresh those routes in a retained environment, then restart
your local port-forward.
The chart’s network policies isolate executors from internal Gen3 services;
fixture policies permit the required MinIO traffic and executor access to the
public Gen3 gateway. Fence advertises that gateway in API-key issuer URLs so
task-token clients can reach both authentication and workflow endpoints. `user.yaml` preserves the
pinned upstream Kind fixture, including read-only access to the main user’s
tasks for the delegated-access test. It also grants the main user `create` on
`/services/fence/task-token/WORKFLOW` for the task-token integration case.
MinIO stores the small test dataset on a 1 GiB memory-backed volume, keeping its
free-space checks independent of the Docker host’s shared disk usage.

The upstream Gen3 Workflow test classes share the same two users and buckets.
Their storage setup and bucket cleanup run once per pytest invocation under a
cross-process lock. Each class waits for setup to finish before executing its
unchanged test bodies. This prevents competing first-time policy creation and
bucket cleanup from overlapping another class's work. Setup errors still fail
the suite; no authorization failures are retried or ignored.

The Go compiler and module caches persist across runs. The candidate image is
built from the checked-out Funnel source. Its identity is checked on both Kind
nodes and against the running server. Workers inherit that image with registry
pulls disabled. The Helm post-renderer prevents the chart's default Funnel image
from replacing the candidate.

The authorizer is built from the pinned
[`funnel-gen3-plugin` commit `eacaf197bfbc786c753d9ecfb86a2a2f7022a5e3`](https://github.com/calypr/funnel-gen3-plugin/commit/eacaf197bfbc786c753d9ecfb86a2a2f7022a5e3)
and the candidate's Funnel module. Both Kind nodes receive that image, and the
verification hook checks the running plugin init container. The renderer requires
both candidate image references to prevent a registry image from replacing either
built artifact.

The pytest controller creates test API keys once before starting its workers.
TES completion polling uses two-second intervals and a 90-attempt limit, with
the existing completion and output assertions unchanged.

Tests stream their output to the job log and artifacts. Diagnostics cover the
Gen3 service namespace and the separate worker namespace. The workflow reserves
time for a final snapshot and uploads artifacts after failure. Inspect the
phase timings, pytest output, pod states, and image identity when a check fails.

The upstream test suite still uses MinIO and the real Mountpoint S3 CSI driver.
The setup script pins CSI chart 2.8.0. Native ARM runs use the upstream
ARM64 digests in `csi-images-arm64.yaml`. The legacy AMD64 configuration uses
images mirrored in `quay.io/ohsu-comp-bio/funnel`
under `ci-csi-*` tags. GitHub-hosted runners exhausted ECR's anonymous data
allowance, so pulling directly from ECR failed before tests could start.

Each mirror preserves the corresponding upstream Linux/amd64 manifest digest.
The Helm image references include that digest, so a changed tag cannot substitute
different image contents.

| Upstream image | Linux/amd64 manifest digest |
| --- | --- |
| `public.ecr.aws/mountpoint-s3-csi-driver/aws-mountpoint-s3-csi-driver:v2.8.0` | `sha256:484def4a3b2eec778ebae1c1574e2b8685df68d316d489fb7dc8d8bef814cb84` |
| `public.ecr.aws/csi-components/csi-node-driver-registrar:v2.17.0-eksbuild.5` | `sha256:aa84e8d445a27744f21d4eb90dc2810e681e1fe4681f0ec60dd0db9454d4aef2` |
| `public.ecr.aws/csi-components/livenessprobe:v2.19.0-eksbuild.5` | `sha256:6bc3e10863a825c6d5c48492bce8c4be4a6582aa77289975bccd17ab164590ed` |

When upgrading, copy the selected upstream manifests with `crane copy`, verify
that `crane digest` returns the same digest for each mirror, and update the chart
version and image references together in `csi-images.yaml` and both Kubernetes
workflows. Verify anonymous mirror pulls before updating the references. The
workflow uses the setup scripts and image values from its own checkout.
