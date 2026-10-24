#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

codec_collector_go="${1:?usage: codec-policy-collect.sh GO CORPUS NEW_OUTPUT}"
codec_collector_corpus="${2:?prepared corpus is required}"
codec_collector_output="${3:?new output directory is required}"
codec_env_names="$(compgen -e)"
while IFS= read -r codec_env_name; do
  case "${codec_env_name}" in GO*|CGO*|MICROFAT*|SOURCE_DATE_EPOCH) unset -v "${codec_env_name}" ;; esac
done <<< "${codec_env_names}"
export GOENV=off GOWORK=off GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOMAXPROCS=4
codec_collector_arch="$("${codec_collector_go}" env GOHOSTARCH)"
export GOARCH="${codec_collector_arch}"
if [[ "${codec_collector_arch}" == arm64 ]]; then export GOARM64=v8.0; else export GOAMD64=v1; fi
mkdir -p .work
timeout --kill-after=10s 600 "${codec_collector_go}" build -mod=readonly -trimpath -buildvcs=false \
  -ldflags='-s -w -buildid=' -o .work/codec-policy-collector ./tests/e2e/testdata/codecpolicy
exec .work/codec-policy-collector -corpus "${codec_collector_corpus}" -output "${codec_collector_output}"
