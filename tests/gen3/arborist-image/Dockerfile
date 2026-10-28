FROM quay.io/cdis/arborist@sha256:308c09045826cc764ca1e4c0b4bf0f6d9b32ac47cabf249ba72e44bc437b9932
USER 0
ARG TARGETARCH
WORKDIR /go/src/github.com/uc-cdis/arborist
RUN test "$TARGETARCH" = arm64 && \
    test "$(git -c safe.directory="$PWD" rev-parse HEAD)" = 506332cc538a12b69ac764db39b50deab31d3aa8 && \
    GITCOMMIT=$(git -c safe.directory="$PWD" rev-parse HEAD) && \
    GITVERSION=$(git -c safe.directory="$PWD" describe --always --tags) && \
    git config --global --add safe.directory "$PWD" && \
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOTOOLCHAIN=go1.26.4 GOSUMDB=sum.golang.org go build \
      -ldflags="-X github.com/uc-cdis/arborist/arborist/version.GitCommit=$GITCOMMIT -X github.com/uc-cdis/arborist/arborist/version.GitVersion=$GITVERSION" \
      -o bin/arborist . && \
    setcap 'cap_net_bind_service=+ep' bin/arborist
USER nobody
