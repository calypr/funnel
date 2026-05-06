---
title: Download
menu:
  main:
    weight: -1000
---

# Overview

| OS                | Archicture            | Supported?                   |
|-------------------|-----------------------|------------------------------|
| [Linux][releases] | ARM64                 | ✅                           |
|                   | AMD64                 | ✅                           |
| [macOS][releases] | ARM64 (Apple Silicon) | ✅                           |
|                   | AMD64 (Intel)         | ✅                           |
| Windows           | ARM64                 | ⚠️ [GitHub Issue][windows] |
|                   | AMD64                 | ⚠️ [GitHub Issue][windows] |

[releases]: https://github.com/ohsu-comp-bio/funnel/releases/latest
[windows]: https://github.com/ohsu-comp-bio/funnel/issues/1258

# Install Options

- [Quick Start (curl one-liner)](#quick-start)
- [Docker](#docker)
- [Podman](#podman)
- [Singularity](#singularity)
- [Homebrew](#homebrew)
- [Git](#git)

## 1. Quick Start

The following command will automatically download and verify the latest version of Funnel for your operating system.

Funnel requires that [Docker](https://docker.io) be installed in order to run commands within a sandboxed environment.

```shell
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/ohsu-comp-bio/funnel/refs/heads/develop/install.sh)"

funnel server run
```

## 2. Containers

The following commands show examples of running Funnel via Docker, Podman, and Singularity.

Each command demonstrates how to mount an optional example config (`example.yaml`) for use by the Funnel Server.

```yaml
# example.yaml ➜ local Funnel Server config
Database: boltdb

Compute: local

Logger:
  Level: debug
```

> [!WARNING]
> When given no config, Funnel will simply run in the default "local" mode.
> 
> This can be helpful for testing and development, but production deployments are recommended to use the more robust database and compute backends available.


### Docker

> [!TIP]
>
> Docker Image → [quay.io/repository/ohsu-comp-bio/funnel:latest](https://quay.io/repository/ohsu-comp-bio/funnel?tab=tags&tag=testing)

```shell
docker run -p 8000:8000 -v example.yaml:/example.yaml quay.io/ohsu-comp-bio/funnel:latest server run --config /example.yaml

curl localhost:8000/service-info
# {
#   "description": "Funnel is a toolkit for distributed task execution via a simple, standard API.",
#   "documentationUrl": "https://ohsu-comp-bio.github.io/funnel/",
#   ...
# }
```

### Podman

> [!TIP]
>
> [Podman: Running a container](https://podman.io/docs#running-a-container)

```shell
podman machine init
# Machine init complete

podman machine start
# Machine "podman-machine-default" started successfully

podman run -p 8000:8000 -v ./example.yaml:/example.yaml quay.io/ohsu-comp-bio/funnel:latest server run --config /example.yaml
# {"httpPort": "8000", "msg": "Server listening", "rpcAddress": ":9090"}
```

### Singularity

> [!TIP]
>
> [Singularity and Docker](https://docs.sylabs.io/guides/2.6/user-guide/singularity_and_docker.html)

```shell
singularity run --bind example.yaml:/example.yaml docker://quay.io/ohsu-comp-bio/funnel:latest server run --config /example.yaml
# INFO:    Converting OCI blobs to SIF format
# INFO:    Starting build...
# INFO:    Creating SIF file...
# server               Server listening
# httpPort             8000
# rpcAddress           :9090
```

## 3. Homebrew

> [!TIP]
>
> Homebrew formula source available at [github.com/ohsu-comp-bio/homebrew-formula](https://github.com/ohsu-comp-bio/homebrew-formula)

```shell
brew tap ohsu-comp-bio/formula

brew install funnel
```

## 4. Git

> [!TIP]
>
> Funnel requires a recent version of Go. See [development docs](../development/developers/) for more detail.

```shell
git clone https://github.com/ohsu-comp-bio/funnel.git

cd funnel

make
```

# Release History

See the [Releases](https://github.com/ohsu-comp-bio/funnel/releases)  page for release history.
