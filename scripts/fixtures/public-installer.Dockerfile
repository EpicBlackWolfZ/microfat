# Official Ubuntu LTS index (native amd64 and arm64); Dependabot maintains the tag and digest.
FROM docker.io/library/ubuntu:26.04@sha256:c098726dd9b9733f3e885f8de0ef375206a311b0a317eadc40d347b1115fb944
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /home/qualification
ENV HOME=/home/qualification PATH=/usr/bin:/bin
USER 10001:10001
