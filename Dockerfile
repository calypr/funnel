# build stage
FROM golang:1.26-alpine AS build-env
RUN apk add make git bash build-base
ENV GOPATH=/go
ENV PATH="/go/bin:${PATH}"

WORKDIR /go/src/github.com/ohsu-comp-bio/funnel
COPY go.* .
RUN go mod download
COPY . .
RUN apk add --no-cache bash build-base git protobuf protobuf-dev
RUN --mount=type=cache,target=/root/.cache/go-build make build

# download nerdctl (Docker-compatible CLI for containerd)
ARG NERDCTL_VERSION=2.2.1
ARG TARGETARCH=amd64
RUN wget -qO /tmp/nerdctl.tgz \
      "https://github.com/containerd/nerdctl/releases/download/v${NERDCTL_VERSION}/nerdctl-${NERDCTL_VERSION}-linux-${TARGETARCH}.tar.gz" && \
    tar -xz -C /tmp -f /tmp/nerdctl.tgz nerdctl

# final stage
FROM alpine
WORKDIR /opt/funnel
EXPOSE 8000 9090
ENV PATH="/app:${PATH}"
# Point nerdctl at the containerd socket mounted from the host node
ENV CONTAINERD_ADDRESS=/run/containerd/containerd.sock
# Use the kubelet's namespace so image pulls are shared with k8s
ENV CONTAINERD_NAMESPACE=k8s.io
COPY --from=build-env /go/src/github.com/ohsu-comp-bio/funnel/funnel /app/
COPY --from=build-env /tmp/nerdctl /usr/local/bin/nerdctl

ENTRYPOINT ["/app/funnel"]
