FROM quay.io/cdis/awshelper@sha256:6d42dcd8bc306554caa4a5b8146ed8a550e0a4b4b0a9fbc0d3f7790856c167b4
USER root
ARG TARGETARCH
RUN test "$TARGETARCH" = arm64 && \
    curl --fail --location https://awscli.amazonaws.com/awscli-exe-linux-aarch64-2.31.35.zip -o /tmp/aws.zip && \
    unzip -q /tmp/aws.zip -d /tmp/aws-native && \
    /tmp/aws-native/aws/install --update && \
    rm -rf /tmp/aws.zip /tmp/aws-native
USER ubuntu
RUN aws --version && kubectl version --client
