---
title: GCP Batch
menu:
  main:
    parent: Compute
    weight: 20
---

# Overview

The following steps illustrate how to run TES tasks via GCP Batch utilizing Google Storage Buckets.

GCS buckets are automatically mounted inside task containers. Input and output paths use standard filesystem paths (e.g. `/input/file.txt`) — no `/mnt/disks/` prefixing required. Nextflow workflows are supported via the TES executor `workdir` field.

# Quick Start

## 1. Download Funnel

```sh
curl -fsSL https://calypr.org/tools/funnel/install.sh | bash
```

## 2. Start Server

<details>
  <summary><code>Config Example</code></summary>

```yaml
Compute: gcp-batch

GCPBatch:
  Project: example-project
  Location: us-central1
```

</details>

```sh
funnel server run --Compute "gcp-batch" --GCPBatch.Project "example-project" --GCPBatch.Location "us-central1"
```

## 3. Submit Task

<details>
  <summary><code>gcp-example.json</code></summary>

```json
{
  "name": "Input/Output Test",
  "inputs": [
    {
      "url": "gs://my-bucket/input/README.md",
      "path": "/input/README.md"
    }
  ],
  "outputs": [
    {
      "url": "gs://my-bucket/output/README.md.sha256",
      "path": "/output/README.md.sha256"
    }
  ],
  "executors": [
    {
      "image": "alpine",
      "command": [
        "sh",
        "-c",
        "sha256sum /input/README.md | tee /output/README.md.sha256"
      ]
    }
  ]
}
```

</details>

```sh
funnel task create gcp-example.json
<TASK ID>
```

## 4. Query Task

```sh
funnel task get <TASK ID>
```

```json
{
  "executors": [
    {
      "command": [
        "sh",
        "-c",
        "sha256sum /input/README.md | tee /output/README.md.sha256"
      ],
      "image": "alpine"
    }
  ],
  "id": "d6f0tgpurbu7o23pgj20",
  "inputs": [
    {
      "path": "/input/README.md",
      "url": "gs://my-bucket/input/README.md"
    }
  ],
  "name": "Input/Output Test",
  "outputs": [
    {
      "path": "/output/README.md.sha256",
      "url": "gs://my-bucket/output/README.md.sha256"
    }
  ],
  "state": "COMPLETE"
}
```

## 5. Verify Outputs

```sh
gsutil cat gs://my-bucket/output/README.md.sha256
9b9916cea5348edd6ad78893231edb81fc96772d1dd99fae9c2a64f84646cb1c  /input/README.md
```

# GPU jobs

`Compute: gcp-batch` selects Funnel's GCP Batch backend. Hardware selection uses the task's native GCP accelerator and machine-type settings. `GCPBatch.Location` selects the Batch job region; it does not select a GPU model.

## Select the hardware and location

Choose compatible accelerator, machine, and location values using Google's documentation:

- [GPU machine types](https://docs.cloud.google.com/compute/docs/gpus) lists GPU offerings and their associated machine types.
- [GPU locations](https://docs.cloud.google.com/compute/docs/regions-zones/gpu-regions-zones) lists where each offering is available. Select a compatible region in `GCPBatch.Location`.
- [Batch GPU jobs](https://docs.cloud.google.com/batch/docs/create-run-job-gpus) describes supported configurations, GPU drivers, images, and container prerequisites.
- [Compute Engine allocation quotas](https://docs.cloud.google.com/compute/resource-usage) describes project quota requirements. Availability in a region does not imply available capacity or sufficient quota.

Funnel passes the requested names to Google Cloud. It does not translate GPU marketing names or maintain a catalogue of compatible combinations. The provider validates the requested combination.

## Request GPUs through TES

For example, add these resource settings to a TES task:

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

The values illustrate the format. Check their compatibility and availability for your project and selected location before submitting.

| Funnel setting | GCP Batch behavior |
| --- | --- |
| `gpu_type` | Sets `allocationPolicy.instances[].policy.accelerators[].type`. |
| `gpu_count` | Sets the accelerator `count`; requires a positive integer. |
| `machine_type` | Sets the instance policy's `machineType`; optional. |
| `install_gpu_drivers` | Sets `allocationPolicy.instances[].installGpuDrivers`; defaults to `true` for GPU requests. |
| `GCPBatch.Location` | Selects the region in the Batch job's parent resource. |

`gpu_type` and `gpu_count` are required together. The optional `machine_type` and `install_gpu_drivers` parameters require that GPU pair. Setting `install_gpu_drivers` to `false` means the selected VM image must already provide the drivers and container tooling required by Google Batch.

## Make GPUs available to the executor

GCP Batch runs Funnel's TES executors directly as container runnables. Funnel adds `--gpus all` to those runnables' container options so they can access the GPUs attached to the job VM. This path does not use the nested Funnel Docker worker or AWS's `NVIDIA_VISIBLE_DEVICES` forwarding mechanism.

See [GPU tasks]({{< ref "gpus.md" >}}) for a complete TES submission example and deployment verification. Request-mapping tests inspect the Batch API payload; physical GPU access still requires a live job on the deployment.

# Nextflow

Nextflow tasks can specify a working directory via the TES executor `workdir` field, which maps to Docker's `--workdir` flag inside the GCP Batch container:

```json
{
  "executors": [
    {
      "image": "nextflow/nextflow:latest",
      "command": ["nextflow", "run", "main.nf"],
      "workdir": "/work"
    }
  ]
}
```

# Additional Resources

- [GCP Batch Documentation](https://docs.cloud.google.com/batch/docs)
