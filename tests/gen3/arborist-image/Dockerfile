FROM quay.io/cdis/arborist@sha256:f874b5918bbecf38a0f6190030aad8d84ac140ab3621c9af73bdcc256c63742f
USER 0
ARG TARGETARCH
WORKDIR /go/src/github.com/uc-cdis/arborist
RUN test "$TARGETARCH" = arm64 && \
    git -c safe.directory="$PWD" checkout --detach 506332cc538a12b69ac764db39b50deab31d3aa8 && \
    test "$(git -c safe.directory="$PWD" rev-parse HEAD)" = 506332cc538a12b69ac764db39b50deab31d3aa8 && \
    GITCOMMIT=$(git -c safe.directory="$PWD" rev-parse HEAD) && \
    GITVERSION=$(git -c safe.directory="$PWD" describe --always --tags) && \
    git config --global --add safe.directory "$PWD" && \
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOTOOLCHAIN=go1.26.4 GOSUMDB=sum.golang.org go build \
      -ldflags="-X github.com/uc-cdis/arborist/arborist/version.GitCommit=$GITCOMMIT -X github.com/uc-cdis/arborist/arborist/version.GitVersion=$GITVERSION" \
      -o bin/arborist . && \
    setcap 'cap_net_bind_service=+ep' bin/arborist
USER nobody
