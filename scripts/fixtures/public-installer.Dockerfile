# Official Ubuntu LTS index (native amd64 and arm64); Dependabot maintains the tag and digest.
FROM docker.io/library/ubuntu:26.04@sha256:d66b366dac61b575bee2aef07e9c26e8070096fbe84a3d4d3eb0da069b5d76db
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /home/qualification
ENV HOME=/home/qualification PATH=/usr/bin:/bin
USER 10001:10001
