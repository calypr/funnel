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
