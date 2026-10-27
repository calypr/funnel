---
title: Deploying a cluster
menu:
  main:
    parent: Compute
    weight: -50
---

# Deploying a cluster

This guide describes the basics of starting a cluster of Funnel nodes. 
This guide is a work in progress.

A node is a service
which runs on each machine in a cluster. The node connects to the Funnel server and reports
available resources. The Funnel scheduler process assigns tasks to nodes. When a task is
assigned, a node will start a worker process. There is one worker process per task.

Nodes aren't always required. In some cases it makes sense to rely on an existing,
external system for scheduling tasks and managing cluster resources, such as AWS Batch,
HTCondor, Slurm, Grid Engine, etc. Funnel provides integration with
these services without using nodes or the scheduler.

### Usage

Nodes are available via the `funnel node` command. To start a node, run
```sh
funnel node run --config node.config.yml
```

To activate the Funnel scheduler, use the `manual` backend in the config.

When upgrading to the task-claim scheduler, stop all older Funnel server
instances before starting the new version. Older schedulers do not consult
claims and can assign the same task to another node. Check the server logs for
conflicting existing assignments before restarting task submission.

With BoltDB, PostgreSQL, and MongoDB, the scheduler records a task claim and its
node reservation together. MongoDB requires a replica set or sharded cluster
for these transactions. Standalone MongoDB deployments must be converted before
upgrading; Funnel rejects them at startup. See the [MongoDB configuration](../../databases/mongodb/).

The available scheduler and node config:
```yaml
# Activate the Funnel scheduler.
Compute: manual

Scheduler:
  # How often to run a scheduler iteration.
  ScheduleRate: 1s

  # How many tasks to schedule in one iteration.
  ScheduleChunk: 10

  # How long to wait between updates before marking a node dead.
  NodePingTimeout: 1m

  # How long to wait for a node to start, before marking the node dead.
  NodeInitTimeout: 5m


Node:
  # If empty, a node ID will be automatically generated using the hostname.
  ID: ""

  # If the node has been idle for longer than the timeout, it will shut down.
  # -1 means there is no timeout. 0 means timeout immediately after the first task.
  Timeout: -1s

  # A Node will automatically try to detect what resources are available to it. 
  # Defining Resources in the Node configuration overrides this behavior.
  Resources:
    # CPUs available.
    # Cpus: 0
    # RAM available, in GB.
    # RamGb: 0.0
    # Disk space available, in GB.
    # DiskGb: 0.0

  # For low-level tuning.
  # How often to sync with the Funnel server.
  UpdateRate: 5s

Logger:
  # Logging levels: debug, info, error
  Level: info
  # Write logs to this path. If empty, logs are written to stderr.
  OutputFile: ""
```

### Assignment storage upgrade

BoltDB, PostgreSQL, and MongoDB use persisted task claims as the authoritative assignment
record. At first startup after this upgrade, Funnel imports active assignments
from existing node records in a transaction. Conflicting owners or a claim with
no reservation on an existing owner prevent startup and must be investigated.
Claims whose owner node has disappeared are retained for scheduler recovery.
The migration records completion and does not rescan all nodes during normal
assignment.

Stop all older Funnel server and scheduler instances before upgrading. Node
heartbeat requests can update node status, but cannot create assignments or
remove active claims by changing `task_ids`. Funnel derives that list from the
claims. Terminal task states remove entries from the active node index while
preserving the durable ownership record.

### Heartbeats, scheduling snapshots, and reservations

For all three scheduler backends, `last_ping` is stored separately from resource
reservations. A routine heartbeat refreshes that timestamp without changing the
node's configuration/reservation version. Assignment and completion do not
refresh liveness. Resource, metadata, drain, and other configuration changes
still check that version. Scheduler health transitions also check the observed
heartbeat before marking a node dead or gone.

Each scheduling pass reads one candidate snapshot. Successful assignments and failed reservation attempts refresh the
affected node in that snapshot. The database validates current eligibility and
capacity before committing an assignment; the snapshot is not authoritative.

An allocation records the resource quantities reserved at assignment. Terminal
state writes release that allocation and update the node's resource usage in
the same database transaction. Repeated terminal writes do not release it twice.
Available capacity is derived from total capacity minus active usage, including
when a node's reported capacity shrinks below its existing reservations.

A completed allocation cannot be reactivated by changing its task back to a
nonterminal state. Submit a new task to retry it. Resource accounting follows
TES terminal states, including cancellation; this does not establish that an
unreachable runtime has physically stopped its process. The design does not add
automatic reassignment or exactly-once process execution.

The startup migration snapshots existing active allocations and reconstructs
resource totals transactionally. PostgreSQL uses short per-task and per-node
transactions during normal reservation/release. MongoDB uses snapshot transactions
with majority writes and retries write conflicts against fresh task and node
records. BoltDB retains its embedded single-writer transaction model. None of
these backends requires an external event log.

### Execution admission

Workers using BoltDB, PostgreSQL, or MongoDB acquire a durable execution claim
before preparing files or starting any executor. The database checks the task
state and records the claim with `RUNNING` in one transaction. Only the worker
that receives a successful acquisition proceeds. A second worker observes the
task until it reaches a terminal state, without preparing files, running an
executor, or writing task events. This prevents a duplicate batch worker from
signaling successful completion while the original execution is still active.
An observer exiting successfully means observation finished; the TES task state
still reports whether execution completed, failed, or was canceled.
A failed claim request or an unsupported claim endpoint cannot authorize a launch.
Local tasks passed directly from a file or the command line do not use this
distributed claim.

Claims have no expiration and do not transfer automatically. If a worker crashes
after acquiring its claim, or loses the successful response, another worker
cannot rerun that task ID. The task and duplicate observers can remain active until a user cancels the
task or the existing backend failure-recovery path records a failure. Inspect the original runtime
before submitting replacement work with a new task ID. Changing the old task's
state or retrying its claim does not grant another execution.

This protects execution admission. It does not recover running containers,
stop an unreachable process, or guarantee exactly-once writes to external
systems. Resource release still follows TES terminal state and does not prove
that an unreachable runtime has stopped.

Stop older servers, workers, and node processes before upgrading. Older workers
do not acquire execution claims and can bypass this protection. Initialize the
database with the upgraded server, then start upgraded workers. An upgraded RPC
worker requires an upgraded server; mixed-version rollouts do not provide this
protection. Existing running
and terminal tasks are treated as having consumed execution admission during
the startup migration.

The claim is an internal worker operation. It does not change the TES task
schema or introduce a public TES endpoint. Worker RPC authentication and task
access rules also apply to acquisition. Database-backed workers using an
unsupported claim implementation fail before execution rather than silently
running without this guard. Datastore, DynamoDB, and Badger do not currently
implement execution admission; use BoltDB, PostgreSQL, or MongoDB for remote
workers with this version. Custom callers of `DefaultWorker` must supply an
`ExecutionClaimer` for non-local task readers.
