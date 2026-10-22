# Official Ubuntu 24.04 index (native amd64 and arm64), reviewed 2026-10-13.
FROM docker.io/library/ubuntu:24.04@sha256:68ee22004a9109b44973313b02ff7b1de250f1de0f5ecb628d73f579aaff33ac
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /home/qualification
ENV HOME=/home/qualification PATH=/usr/bin:/bin
USER 10001:10001
