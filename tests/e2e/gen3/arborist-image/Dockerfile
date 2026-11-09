FROM quay.io/cdis/arborist:4.1.2@sha256:70675dd573ce31cab098e45d60e6c5983ebf089faa39015a17002db81ea9778a
USER 0
ARG TARGETARCH
WORKDIR /go/src/github.com/uc-cdis/arborist
RUN case "$TARGETARCH" in amd64|arm64) ;; *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; esac && \
    test "$(git -c safe.directory="$PWD" rev-parse HEAD)" = ccf67c19c381edc72a13fea58adc42b83af003e8 && \
    GITCOMMIT=$(git -c safe.directory="$PWD" rev-parse HEAD) && \
    GITVERSION=$(git -c safe.directory="$PWD" describe --always --tags) && \
    git config --global --add safe.directory "$PWD" && \
    CGO_ENABLED=0 GOOS=linux GOARCH="$TARGETARCH" GOTOOLCHAIN=go1.26.4 GOSUMDB=sum.golang.org go build \
      -ldflags="-X github.com/uc-cdis/arborist/arborist/version.GitCommit=$GITCOMMIT -X github.com/uc-cdis/arborist/arborist/version.GitVersion=$GITVERSION" \
      -o bin/arborist . && \
    setcap 'cap_net_bind_service=+ep' bin/arborist
USER nobody
