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

With BoltDB and PostgreSQL, the scheduler records a task claim and its node
reservation together. MongoDB retains its existing scheduler behavior and does
not provide the same concurrent-assignment guarantee.

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
