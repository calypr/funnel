---
title: GPU tasks
weight: 12
---

Funnel accepts GPU settings through TES `resources.backend_parameters`. The supported keys depend on the compute backend. Funnel passes hardware names to that backend; it does not maintain a GPU model catalogue.

Set `backend_parameters_strict` to `true` when the task requires these settings. Unsupported strict parameters reject task creation. Unsupported optional parameters produce a server warning and are removed from the stored task. Parameter names are case-insensitive. Supplying the same name with different casing is an error.

Use `GET /v1/service-info` to read `tesResources_backend_parameters`, the list of supported keys for this deployment.

## Submit a GPU task

Each backend example below supplies the `resources` portion of a TES task. Add it to a task with a compatible container image and command. For example, save this local Docker task as `gpu-task.json`:

```json
{
  "name": "GPU visibility check",
  "resources": {
    "backend_parameters_strict": true,
    "backend_parameters": {"gpu_count": "1"}
  },
  "executors": [
    {"image": "ubuntu:22.04", "command": ["nvidia-smi", "-L"]}
  ]
}
```

The NVIDIA runtime must provide `nvidia-smi` and driver access to this container. For another backend, replace `resources` with that backend's example and use the GPU image required by the deployment.

Submit the file to Funnel:

```sh
curl --fail-with-body -X POST http://localhost:8000/v1/tasks \
  -H 'Content-Type: application/json' \
  --data-binary @gpu-task.json
```

Use the returned task ID to inspect its status and executor logs:

```sh
curl --fail-with-body 'http://localhost:8000/v1/tasks/TASK_ID?view=FULL'
```

Replace the endpoint and authentication options with those for your deployment. A successful task reports the visible GPU identities in its executor stdout.

## Kubernetes

```json
{
  "resources": {
    "backend_parameters_strict": true,
    "backend_parameters": {
      "gpu_count": "1",
      "gpu_resource": "nvidia.com/gpu",
      "gpu_node_selector": "accelerator=a100"
    }
  }
}
```

`gpu_count` is a positive integer. `gpu_resource` defaults to `nvidia.com/gpu`. `gpu_node_selector` accepts comma-separated `key=value` node labels, such as `accelerator=a100,vendor=nvidia`. The cluster administrator defines these labels. These labels select the required hardware. A conflicting administrator or executor-template selector is rejected.

Funnel sets the GPU request and limit on the executor container, including when an administrator supplies a custom executor Job template. The worker and sidecar containers do not receive the task's GPU request. A conflicting GPU request or limit already present in the executor template is rejected.

The cluster requires the appropriate GPU device plugin, drivers, and container runtime configuration. Kubernetes performs placement and device allocation. A resource request can remain pending when matching capacity is unavailable.

## AWS Batch

```json
{
  "resources": {
    "backend_parameters_strict": true,
    "backend_parameters": {
      "gpu_count": "2",
      "job_queue": "a100-queue",
      "job_definition": "funnel-gpu-worker:2"
    }
  }
}
```

`gpu_count` adds a Batch GPU resource requirement. Optional `job_queue` and `job_definition` override the configured defaults. Each accepts an AWS resource name or ARN. The server's AWS identity must have permission to submit the selected job. Administrators control which hardware is available through the queue's compute environments and allowed instance types.

Batch reserves GPUs for the Funnel worker container. Funnel launches task containers through the host Docker socket, so it must also forward the devices allocated to the worker. Funnel reads Batch's `NVIDIA_VISIBLE_DEVICES` allocation and requests those exact devices for the executor. A missing or incompatible allocation fails execution instead of selecting arbitrary host GPUs.

The Batch worker command must include `--Compute aws-batch`. Newly generated Funnel job definitions include it. Update existing job definitions before submitting GPU tasks.

Use a GPU-capable compute environment, worker image, Docker runtime, and task image. GPU scheduling in AWS Batch supports NVIDIA hardware. Custom Docker run templates must include `{{.GPUArgs}}`.

## GCP Batch

```json
{
  "resources": {
    "backend_parameters_strict": true,
    "backend_parameters": {
      "gpu_type": "nvidia-tesla-t4",
      "gpu_count": "1",
      "machine_type": "n1-standard-4",
      "install_gpu_drivers": "true"
    }
  }
}
```

`gpu_type` and `gpu_count` are required together. The type is a native GCP accelerator name and the count is a positive integer. `machine_type` optionally selects a compatible VM type. `install_gpu_drivers` defaults to `true`; set it to `false` only when the VM image already provides the required drivers and container toolkit. Both optional fields require a GPU request.

Funnel sets the Batch allocation policy and enables GPU access on each executor runnable. The selected location, machine type, accelerator, quota, and image must be compatible. GCP validates their compatibility.

## Slurm

The administrator exposes native Slurm settings through a validated allowlist and a submission template. For example:

```yaml
Slurm:
  BackendParameters:
    gres: '[A-Za-z0-9_:-]+'
    partition: '[A-Za-z0-9_-]+'
  TemplateFile: /etc/funnel/slurm-template.txt
```

Each regular expression must match the entire parameter value. Values containing control characters are rejected. Use narrow expressions because parameter values become part of the scheduler's submission file. Only Slurm loads this allowlist; the shared configuration type's `BackendParameters` field is not supported by PBS or HTCondor.

Add these directives to the submission template:

```text
{{with index .BackendParameters "gres"}}#SBATCH --gres={{.}}{{end}}
{{with index .BackendParameters "partition"}}#SBATCH --partition={{.}}{{end}}
```

TES resource settings for a deployment with an `a100` GRES type and a `gpu` partition:

```json
{
  "resources": {
    "backend_parameters_strict": true,
    "backend_parameters": {"gres": "gpu:a100:2", "partition": "gpu"}
  }
}
```

Templates receive lowercase parameter names. The Slurm administrator defines the supported GRES types and partitions. The worker must load the Slurm configuration, for example through `funnel worker run --Compute slurm --taskID ...`. The default Slurm template includes this flag.

Before starting the worker, the template or a site allocation hook must export `FUNNEL_GPU_DEVICES`. Its value is a comma-separated list of allocated physical GPU IDs or UUIDs as seen by the host Docker daemon. Prefer UUIDs. Slurm's `CUDA_VISIBLE_DEVICES` indices can be remapped inside cgroups; do not copy them blindly as host Docker device IDs. Funnel forwards `FUNNEL_GPU_DEVICES` to Docker.

A submission directive alone reserves resources but does not provide container access. Verify the allocation hook on the cluster. Custom Docker run templates must include `{{.GPUArgs}}`.

## Local Docker

Local execution accepts `gpu_count` or `gpu_devices`. `gpu_devices` is a comma-separated list of physical GPU indices or NVIDIA GPU UUIDs. If both fields are present, the count must match the list length. Local Docker requires the NVIDIA Container Toolkit and compatible drivers. Local execution does not reserve GPUs between concurrent tasks.

Local TES resource settings for two specific devices:

```json
{
  "resources": {
    "backend_parameters_strict": true,
    "backend_parameters": {"gpu_devices": "0,2"}
  }
}
```

To let Docker select two devices instead, use `"backend_parameters": {"gpu_count": "2"}`. Start Funnel with `--Compute local` for these examples.

## Unsupported backends

GPU backend parameters are not supported by PBS, Grid Engine, HTCondor, or Funnel's manual scheduler. These backends advertise no GPU keys. Set `backend_parameters_strict` to `true` to reject a task that requires unsupported settings. Optional unsupported settings produce a warning and are removed according to TES semantics.

## Deployment verification

Request propagation tests do not prove that a physical GPU is available. Before using a deployment for GPU workflows, submit a task with its required GPU parameters and a compatible image that runs `nvidia-smi -L`. Confirm that the model and number of visible devices match the request. Also run concurrent tasks to verify that the scheduler allocates distinct devices where exclusive allocation is expected.
