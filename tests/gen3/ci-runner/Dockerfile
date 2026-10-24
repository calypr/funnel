FROM docker:29.4.1-dind AS docker
FROM ubuntu:24.04
ARG TARGETARCH
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends bash ca-certificates curl git jq iptables iproute2 netcat-openbsd procps python3 tar xz-utils unzip kmod && rm -rf /var/lib/apt/lists/*
COPY --from=docker /usr/local/bin/docker /usr/local/bin/dockerd /usr/local/bin/containerd /usr/local/bin/containerd-shim-runc-v2 /usr/local/bin/ctr /usr/local/bin/runc /usr/local/bin/docker-init /usr/local/bin/docker-proxy /usr/local/bin/dind /usr/local/bin/
COPY --from=docker /usr/local/libexec/docker/cli-plugins/ /usr/local/libexec/docker/cli-plugins/
RUN test "$TARGETARCH" = arm64 && \
    curl -fsSL https://kind.sigs.k8s.io/dl/v0.31.0/kind-linux-arm64 -o /usr/local/bin/kind && \
    curl -fsSL https://dl.k8s.io/release/v1.35.0/bin/linux/arm64/kubectl -o /usr/local/bin/kubectl && \
    curl -fsSL https://get.helm.sh/helm-v4.3.1-linux-arm64.tar.gz -o /tmp/helm.tgz && tar -xzf /tmp/helm.tgz -C /tmp && mv /tmp/linux-arm64/helm /usr/local/bin/helm && \
    curl -fsSL https://github.com/mikefarah/yq/releases/download/v4.45.2/yq_linux_arm64 -o /usr/local/bin/yq && \
    curl -fsSL https://github.com/astral-sh/uv/releases/download/0.11.15/uv-aarch64-unknown-linux-gnu.tar.gz -o /tmp/uv.tgz && tar -xzf /tmp/uv.tgz -C /tmp && mv /tmp/uv-aarch64-unknown-linux-gnu/uv* /usr/local/bin/ && \
    chmod +x /usr/local/bin/kind /usr/local/bin/kubectl /usr/local/bin/yq && rm /tmp/*.tgz
RUN curl -fsSL https://github.com/astral-sh/python-build-standalone/releases/download/20261020/cpython-3.13.16%2B20261020-aarch64-unknown-linux-gnu-install_only_stripped.tar.gz -o /tmp/python.tar.gz && \
    echo '83621f7b2b1e5da63cae0b4c67612974547b14262676608e830e246cb832d07d  /tmp/python.tar.gz' | sha256sum --check && \
    tar -xzf /tmp/python.tar.gz -C /opt && rm /tmp/python.tar.gz
ENV PATH=/opt/python/bin:$PATH
RUN curl -fsSL https://awscli.amazonaws.com/awscli-exe-linux-aarch64-2.31.35.zip -o /tmp/aws.zip && \
    unzip -q /tmp/aws.zip -d /tmp && /tmp/aws/install && rm -rf /tmp/aws /tmp/aws.zip
RUN apt-get update && apt-get install -y --no-install-recommends build-essential openjdk-21-jre-headless && rm -rf /var/lib/apt/lists/*
ENV CC=gcc
COPY tests/gen3/ci-runner/entrypoint.sh /usr/local/bin/gen3-ci-runner
ENTRYPOINT ["bash", "/usr/local/bin/gen3-ci-runner"]
CMD ["serve"]
