---
title: Slurm
menu:
  main:
    parent: Compute
    weight: 20
---
# Slurm

Funnel can be configured to submit workers to [Slurm][slurm] by making calls
to `sbatch`.

The Funnel server needs to run on a submission node.

## Files and connectivity

For each task, the server creates a directory under `Worker.WorkDir` and writes
a Slurm submission script there. The default script also puts the Slurm stdout
and stderr files in that task directory. Use an absolute `Worker.WorkDir` path
that is accessible and writable at the same location on the submission host
and the compute nodes, for example a shared filesystem:

```yaml
Worker:
  WorkDir: /shared/funnel/work
```

Use the same setting in the worker's configuration. The `funnel` executable,
the worker configuration file, and the container runtime must also be available
on compute nodes. If the worker uses an embedded server database such as BoltDB
or Badger, it reads tasks and reports events over RPC: configure
`RPCClient.ServerAddress` to an address reachable from the compute nodes.
For other database backends, the worker must be able to reach the configured
database and storage services.

The shared working directory does not mean every input or output must be on
the shared filesystem. The worker stages task inputs from their URLs, runs the
container, and transfers declared outputs to their URLs. For `file://` URLs,
the referenced paths must be accessible on the compute node and permitted by
`LocalStorage.AllowedDirs`; see [local storage](../../storage/local/).

Workers normally remove their task working directory when they finish. Failed
submissions can leave directories behind; check them before cleaning them up.
If you customize the Slurm template, ensure its log paths and any config path
passed to the worker are accessible from the compute node.

## Slurm configuration

It is recommended to update the submit file template so that the
`funnel worker run` command takes a config file as an argument 
(e.g. `funnel worker run --config /opt/funnel_config.yml --taskID {{.TaskId}}`)

```YAML
{{< slurm-template >}}
```
The following variables are available for use in the template:

| Variable    |  Description |
|:------------|:-------------|
|TaskId       | funnel task id |
|WorkDir      | funnel working directory |
|Cpus         | requested cpu cores |
|RamGb        | requested ram |
|DiskGb       | requested free disk space |
|Zone         | requested zone (could be used for queue name) |

See https://golang.org/pkg/text/template for information on creating templates.

[slurm]: https://slurm.schedmd.com/
