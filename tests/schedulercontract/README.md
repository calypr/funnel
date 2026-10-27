# Scheduler contract tests

This suite runs the same scheduler scenarios against real BoltDB and PostgreSQL
stores. It covers concurrent assignment, resource contention, retry on another
node, recovery after a committed reservation, and failed state writes or lost
responses. The tests run Funnel's scheduler directly; they do not launch task
workers.

Run both backends locally with Docker available for PostgreSQL:

```sh
go test -race -v ./tests/schedulercontract -count=1
```

Select one backend:

```sh
FUNNEL_SCHEDULER_TEST_BACKEND=boltdb go test -race -v ./tests/schedulercontract -count=1
FUNNEL_SCHEDULER_TEST_BACKEND=postgres go test -race -v ./tests/schedulercontract -count=1
```

Each case gets an isolated store. PostgreSQL uses a temporary Docker container
and a separate database per case. The suite removes its temporary resources when
it finishes. Unknown backend names fail the test.

CI runs `Scheduler tests (BoltDB)` and `Scheduler tests (PostgreSQL)` as separate
matrix jobs, with the race detector and per-backend JUnit artifacts. The general
Go test job skips `TestSchedulerContract` because the matrix runs it for both
backends. The required `unitTest` check depends on the general tests and the
scheduler matrix.

To add a backend, provide a fixture implementing the suite's store interface and
register it in `TestSchedulerContract`. Add the backend to the workflow matrix;
it will run every common scenario. Keep tests requiring private storage access,
such as transaction rollback injection, in the backend's own package.
