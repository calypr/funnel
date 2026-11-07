FROM quay.io/cdis/awshelper:2026.11@sha256:24d0c7ac0ca3e26abb70f63c08ff8d75dd2863eb8efdc8b4d5136e0f9cfab075
USER root
ARG TARGETARCH
RUN case "$TARGETARCH" in \
      amd64) aws_arch=x86_64 ;; \
      arm64) aws_arch=aarch64 ;; \
      *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac && \
    curl --fail --location "https://awscli.amazonaws.com/awscli-exe-linux-${aws_arch}-2.31.35.zip" -o /tmp/aws.zip && \
    unzip -q /tmp/aws.zip -d /tmp/aws-native && \
    /tmp/aws-native/aws/install --update && \
    rm -rf /tmp/aws.zip /tmp/aws-native
USER ubuntu
RUN aws --version && kubectl version --client
