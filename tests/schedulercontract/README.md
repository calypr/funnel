# Scheduler contract tests

This suite runs the same scheduler scenarios against real BoltDB, PostgreSQL,
and MongoDB stores. It covers concurrent assignment, resource contention, retry on another
node, recovery after a committed reservation, and failed state writes or lost
responses. It also covers heartbeat fencing, capacity changes, duplicate terminal
writes, and one node snapshot per batch. The tests run Funnel's scheduler
directly; they do not launch task workers.

Run all three backends locally with Docker available for PostgreSQL and MongoDB:

```sh
go test -race -v ./tests/schedulercontract -count=1
```

Select one backend:

```sh
FUNNEL_SCHEDULER_TEST_BACKEND=boltdb go test -race -v ./tests/schedulercontract -count=1
FUNNEL_SCHEDULER_TEST_BACKEND=postgres go test -race -v ./tests/schedulercontract -count=1
FUNNEL_SCHEDULER_TEST_BACKEND=mongodb go test -race -v ./tests/schedulercontract -count=1
```

Each case gets an isolated store. PostgreSQL and MongoDB each use a temporary
Docker container and a separate database per case. MongoDB runs as a real
single-node replica set so the suite exercises multi-document transactions.
The suite removes its temporary resources when it finishes. Unknown backend names fail the test.

CI runs `Scheduler tests (BoltDB)`, `Scheduler tests (PostgreSQL)`, and
`Scheduler tests (MongoDB)` as separate matrix jobs, with the race detector and
per-backend JUnit artifacts. The general Go test job skips `TestSchedulerContract` because the matrix runs it for all three
backends. The required `unitTest` check depends on the general tests and the
scheduler matrix.

To add a backend, provide a fixture implementing the suite's store interface and
register it in `TestSchedulerContract`. Add the backend to the workflow matrix;
it will run every common scenario. Keep tests requiring private storage access,
such as transaction rollback injection, in the backend's own package.
