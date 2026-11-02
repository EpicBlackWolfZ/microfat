# Official Ubuntu LTS index (native amd64 and arm64); Dependabot maintains the tag and digest.
FROM docker.io/library/ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /home/qualification
ENV HOME=/home/qualification PATH=/usr/bin:/bin
USER 10001:10001
