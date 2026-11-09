# Continuous integration

PR checks separate test environments while keeping database startup out of the
general Go race suite's shared Docker workload.

| Check | Coverage |
| --- | --- |
| Go tests | All Go packages except the four packages assigned to the scheduler matrix; race detector, coverage, two packages at a time |
| Scheduler tests: BoltDB | Scheduler contract against BoltDB |
| Scheduler tests: PostgreSQL | PostgreSQL package tests, scheduler contract, and independent process/crash recovery tests, sequentially |
| Scheduler tests: MongoDB | MongoDB package tests, scheduler contract, and independent process/crash recovery tests, sequentially |
| Docker worker integration | Resource limits, cancellation, execution admission, and task output contracts with the Docker integration gate enabled |
| Local storage executor permissions | The executor ownership test under root, which creates and tests distinct user identities |
| Public API | A separate Go module consuming the TES client and plugin SDK |
| Dashboard | JavaScript tests, production bundle consistency, and browser workflows against Funnel |
| Kubernetes storage | PVC/S3 storage and failure recovery in k3d |
| Nextflow | Local execution and Kubernetes/S3 execution in separate environments |
| Gen3 | Authentication, authorization, storage, and workflows through the Gen3 stack |
| Compliance | External TES 1.0 and 1.1 compliance suites from a pinned runner revision |

Static analysis, vulnerability checks, website builds, and multi-platform image
builds provide additional checks. The image publishing workflow and compliance
workflow run on pushes; most test workflows run on PRs and pushes to `develop`.
Some workflows have narrower branch or path filters; their triggers are defined
under `.github/workflows`.

The four Go packages assigned outside the general suite are
`internal/database/postgres`, `internal/database/mongodb`,
`tests/integration/schedulercontract`, and `tests/e2e/schedulerprocess`.
Their JUnit reports and coverage profiles are uploaded by the scheduler matrix.
The general suite uploads its own report and coverage profile.

The `unitTest` and `Integration tests / integration_tests` jobs preserve existing
required status names. They aggregate results and do not run tests again.

## Startup and failure handling

Database and worker images are pulled before tests start. Image downloads have
bounded retries; assertions and complete test runs are not automatically retried.
Database readiness checks remain in place, and startup deadlines are separate
from the database operation deadlines. Keep the pre-pulled Ryuk version aligned
with the version used by `testcontainers-go`.

Failed Go jobs upload Docker daemon information, image inventory, and available
test-container state and logs. Full container inspection is deliberately omitted
because it includes credential environment variables. Test fixtures clean up
containers even when startup fails, so a removed container's logs may only be
available in the test output.

The in-process test harness reserves its actual server sockets until startup and
releases them on cleanup. Node-drain tests wait for observed lifecycle states and
drive scheduler passes explicitly. PostgreSQL initialization also cancels stalled
connections when its initialization budget expires; a retry budget alone cannot
interrupt a blocked network operation.

All CI jobs have explicit time limits. Network download retries are bounded, and
readiness probes fail when their wait budget expires. The reusable binary cache
is keyed by OS, architecture, and commit to include embedded assets and build
metadata in its identity.

Run the CI helper regressions locally with GNU `timeout` available:

```sh
python3 .github/scripts/test-ci-helpers.py
```
