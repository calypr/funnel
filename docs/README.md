# Overview

Funnel makes distributed, batch processing easier by providing a simple task API and a set of
components which can easily adapted to a vareity of platforms.

For an existing deployment or Go integration, follow the [Funnel upgrade guide](upgrading.md)
before moving to the unreleased package and configuration changes.

### Task

A task defines a unit of work: metadata, input files to download, a sequence of Docker containers + commands to run,
output files to upload, state, and logs. The API allows you to create, get, list, and cancel tasks.

Tasks are accessed via the `funnel task` command. Go applications use the
[REST client][clientpkg] and [TES wire models][tespkg].

There's a lot more you can do with the task API. See the [tasks docs](tasks.md) for more.

### Server

The server serves the task API, web dashboard, and optionally runs a task scheduler.
It serves both HTTP/JSON and gRPC/Protobuf.

Run the server with `funnel server`. Its implementation lives in `internal/server`.

### Storage

Storage provides access to file systems such as S3, Google Storage, and local filesystems.
Tasks define locations where files should be downloaded from and uploaded to. Workers handle
the downloading/uploading.

See the [storage docs](storage.md) for more information on configuring storage backends.
Storage implementations live in `internal/storage` and are private application code.

### Worker

A worker is reponsible for executing a task. There is one worker per task. A worker:

- downloads the inputs
- runs the sequence of executors (usually via Docker)
- uploads the outputs

Along the way, the worker writes logs to event streams and databases:

- start/end time
- state changes (initializing, running, error, etc)
- executor start/end times
- executor exit codes
- executor stdout/err logs
- a list of output files uploaded, with sizes
- system logs, such as host name, docker command, system error messages, etc.

Run a worker with `funnel worker`. Its runtime lives in `internal/worker`, with
task path mapping in `internal/worker/workspace`.

### Node Scheduler

A node is a service that stays online and manages a pool of task workers. A Funnel cluster
runs a node on each VM. Nodes communicate with a Funnel scheduler, which assigns tasks
to nodes based on available resources. Nodes handle starting workers when for each assigned
task.

Nodes aren't always required. In some cases it often makes sense to rely on an existing,
external system for scheduling tasks and managing cluster resources, such as AWS Batch
or HPC systems like HTCondor, Slurm, Grid Engine, etc. Funnel provides integration with
these services that doesn't include nodes or scheduling by Funnel.

See [Deploying a cluster](compute/deployment.md) for more information about running a cluster of nodes.

Run a node with `funnel node`. Its implementation lives in `internal/compute/scheduler`.

[tes]: https://github.com/ga4gh/task-execution-schemas
[clientpkg]: https://github.com/calypr/funnel/tree/feature/client-server-split/client/tes
[tespkg]: https://github.com/calypr/funnel/tree/feature/client-server-split/api/tes
