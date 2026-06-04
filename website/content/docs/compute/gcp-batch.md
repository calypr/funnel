---
title: GCP Batch
menu:
  main:
    parent: Compute
    weight: 20
---

> [!WARNING]
> Current Limitations: ⚠️
> 
> - Latest Funnel release ([v0.11.11](https://github.com/ohsu-comp-bio/funnel/releases/tag/v0.11.11)) requires specific bucket prefixing in the inputs and outputs
>   - e.g. `/mnt/disks/<BUCKET>/<FILE>` instead of `/<FILE>`
> - Nextflow workflows are currently not supported (as Nextflow expects root-level working directories → `/work`)

# Overview

The following steps illustrate how to run a TES tasks via GCP Batch utilizing Google Storage Buckets.

# Quick Start

## 1. Download Funnel

```sh
curl -fsSL https://ohsu-comp-bio.github.io/funnel/install.sh | bash
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
      "url": "gs://tes-batch-integration/README.md",
      "path": "/mnt/disks/tes-batch-integration/README.md"
    }
  ],
  "outputs": [
    {
      "url": "gs://tes-batch-integration/README.md.sha256",
      "path": "/mnt/disks/tes-batch-integration/README.md.sha256"
    }
  ],
  "executors": [
    {
      "image": "alpine",
      "command": [
        "sha256sum",
        "/mnt/disks/tes-batch-integration/README.md | tee /mnt/disks/tes-batch-integration/README.md.sha256"
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
        "sha256sum",
        "/mnt/disks/tes-batch-integration/README.md | tee /mnt/disks/tes-batch-integration/README.md.sha256"
      ],
      "image": "alpine"
    }
  ],
  "id": "d6f0tgpurbu7o23pgj20",
  "inputs": [
    {
      "path": "/mnt/disks/tes-batch-integration/README.md",
      "url": "gs://tes-batch-integration/README.md"
    }
  ],
  "name": "GCP Batch Task Example",
  "outputs": [
    {
      "path": "/mnt/disks/tes-batch-integration/README.md.sha256",
      "url": "gs://tes-batch-integration/README.md.sha256"
    }
  ],
  "state": "COMPLETE"
}
```

## 5. Verify Outputs

```sh
gsutil cat gs://tes-batch-integration/README.md.sha256
9b9916cea5348edd6ad78893231edb81fc96772d1dd99fae9c2a64f84646cb1c  /mnt/disks/tes-batch-integration/README.md
```

# Additional Resources

- https://docs.cloud.google.com/batch/docs
