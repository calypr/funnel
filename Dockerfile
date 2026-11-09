# build stage
FROM golang:1.27.3-alpine AS build-env
RUN apk add make git bash build-base
ENV GOPATH=/go
ENV PATH="/go/bin:${PATH}"

WORKDIR /go/src/github.com/ohsu-comp-bio/funnel
COPY go.* .
COPY .github/scripts/download-go-modules.sh /tmp/download-go-modules.sh
RUN sh /tmp/download-go-modules.sh
COPY . .
RUN apk add --no-cache bash build-base git protobuf protobuf-dev
ARG FUNNEL_VERSION
ARG GIT_COMMIT
RUN --mount=type=cache,target=/root/.cache/go-build \
    if [ -n "$FUNNEL_VERSION" ]; then \
      make build FUNNEL_VERSION="$FUNNEL_VERSION" git_commit="$GIT_COMMIT"; \
    else \
      make build; \
    fi

# download nerdctl (Docker-compatible CLI for containerd)
ARG NERDCTL_VERSION=2.4.2
ARG TARGETARCH
RUN apk add --no-cache curl && \
    curl --fail --silent --show-error --location \
      --retry 4 --retry-max-time 180 --connect-timeout 15 --max-time 60 \
      --output /tmp/nerdctl.tgz \
      "https://github.com/containerd/nerdctl/releases/download/v${NERDCTL_VERSION}/nerdctl-${NERDCTL_VERSION}-linux-${TARGETARCH}.tar.gz" && \
    tar -xz -C /tmp -f /tmp/nerdctl.tgz nerdctl && \
    test "$(go version -m /tmp/nerdctl | sed -n 's/.*GOARCH=//p')" = "$(go env GOARCH)"

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
