---
title: Download
menu:
  main:
    weight: -1000
---

# Download

> [!NOTE]
>
> Funnel requires that [Docker](https://docker.io) be installed in order to run commands within a sandboxed environment.

## 1. Quick Start

Run the following [install script](https://github.com/ohsu-comp-bio/funnel/blob/develop/install.sh) to fetch latest version of Funnel from [GitHub Releases](https://github.com/ohsu-comp-bio/funnel/releases):

```sh
curl -fsSL https://ohsu-comp-bio.github.io/funnel/install.sh | bash
```

## 2. Containers

### Docker

> [!TIP]
>
> Docker Image → [quay.io/repository/ohsu-comp-bio/funnel:latest](https://quay.io/repository/ohsu-comp-bio/funnel?tab=tags&tag=testing)

```sh
docker run -p 8000:8000 quay.io/ohsu-comp-bio/funnel:latest server run

# With config
docker run -p 8000:8000 -v ./config.yaml:/config.yaml quay.io/ohsu-comp-bio/funnel:latest server run --config /config.yaml
```

### Podman

> [!TIP]
>
> [Podman: Running a container](https://podman.io/docs#running-a-container)

```sh
podman run -p 8000:8000 quay.io/ohsu-comp-bio/funnel:latest server run

# With config
podman run -p 8000:8000 -v ./config.yaml:/config.yaml quay.io/ohsu-comp-bio/funnel:latest server run --config /config.yaml
```

### Singularity

> [!TIP]
>
> [Singularity and Docker](https://docs.sylabs.io/guides/2.6/user-guide/singularity_and_docker.html)

```sh
singularity run docker://quay.io/ohsu-comp-bio/funnel:latest server run 

# With config
singularity run --bind config.yaml:/config.yaml docker://quay.io/ohsu-comp-bio/funnel:latest server run --config /config.yaml
```
