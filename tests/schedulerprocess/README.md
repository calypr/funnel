# Scheduler process integration tests

These tests run independent Funnel server and node processes against a real PostgreSQL database or MongoDB replica set. The node processes start production workers in their task goroutines. Separate worker processes exercise the execution-admission crash boundary. The tests cover concurrent scheduling, resource reservations, process recovery, and durable worker admission.

## Run the tests

Start Docker, then select one backend:

```sh
FUNNEL_TEST_SCHEDULER_PROCESS_INTEGRATION=1 FUNNEL_SCHEDULER_TEST_BACKEND=postgres go test -race ./tests/schedulerprocess -run '^TestSchedulerProcesses$' -count=1 -timeout=10m
FUNNEL_TEST_SCHEDULER_PROCESS_INTEGRATION=1 FUNNEL_SCHEDULER_TEST_BACKEND=mongodb go test -race ./tests/schedulerprocess -run '^TestSchedulerProcesses$' -count=1 -timeout=10m
```

The suite skips unless `FUNNEL_TEST_SCHEDULER_PROCESS_INTEGRATION=1`. `FUNNEL_SCHEDULER_TEST_BACKEND` must be `postgres` or `mongodb` when the gate is enabled. Docker runs the database fixture and the `alpine:3.21` task containers.

## Scenarios

The capacity scenario starts two independent Funnel servers and two node processes. It submits six tasks through the TES gRPC API. Each real Docker task writes a launch marker and holds its slot until the test releases it. The test checks the persisted reservations while both slots are full, verifies that queued tasks do not launch, releases slots in waves, counts one launch per task, and waits for every task to reach `COMPLETE`.

The scheduler recovery scenario runs the production scheduler in a helper process with a real database client. A test wrapper calls the backend's real `TryAssign`, signals after the transaction commits, and blocks before the scheduler writes `INITIALIZING`. The parent kills that process, checks the stored claim and reservation, then starts a production server scheduler and node. The recovered task launches once and completes.

The worker recovery scenario runs `cmd/worker.NewWorker` in a helper process. A test wrapper delegates to the real `ClaimExecution`, signals after admission commits, and blocks before executor startup. The parent kills that process, confirms that the task remains `RUNNING` and its node reservation remains active, then starts a second production worker. The second worker observes `RUNNING`, does not launch an executor, and exits after the test cancels the task.

The test binary supplies the helper process entry point. The helpers run production server, scheduler, node, and worker code with independent database clients. The suites exercise normal database initialization needed to start those processes, but they do not test schema upgrades or migration behavior.

Each child process has a bounded startup or observation wait and cleanup registered as soon as it starts. Failure output includes child stdout and stderr, task states, node reservations, and launch markers. Docker cleanup targets only the task containers named by their generated task IDs.
