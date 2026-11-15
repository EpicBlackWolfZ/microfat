# Official Ubuntu LTS index (native amd64 and arm64); Dependabot maintains the tag and digest.
FROM docker.io/library/ubuntu:26.04@sha256:065aa8626841f16057b78b027b464cd59c4fac36c204c72dcbf48d66f5fa8a96
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /home/qualification
ENV HOME=/home/qualification PATH=/usr/bin:/bin
USER 10001:10001
