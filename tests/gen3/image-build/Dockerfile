ARG GO_BUILDER_IMAGE=golang:1.26-alpine

FROM ${GO_BUILDER_IMAGE} AS go-builder

RUN apk add --no-cache make git bash build-base protobuf protobuf-dev

ENV GOPATH=/go
ENV PATH="/go/bin:${PATH}"

ARG WORKSPACE_DIR=/workspace
ARG NERDCTL_VERSION=2.2.1
ARG TARGETARCH

WORKDIR ${WORKSPACE_DIR}

RUN wget -qO /tmp/nerdctl.tgz \
      "https://github.com/containerd/nerdctl/releases/download/v${NERDCTL_VERSION}/nerdctl-${NERDCTL_VERSION}-linux-${TARGETARCH}.tar.gz" && \
    tar -xz -C /tmp -f /tmp/nerdctl.tgz nerdctl

FROM alpine:3.22 AS gen3-plugin

COPY authorizer-binary /app/build/plugins/authorizer

RUN test -f /app/build/plugins/authorizer && test -x /app/build/plugins/authorizer
