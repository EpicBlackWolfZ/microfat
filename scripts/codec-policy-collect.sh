#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

codec_collector_go="${1:?usage: codec-policy-collect.sh GO CORPUS NEW_OUTPUT [original|isolated]}"
codec_collector_corpus="${2:?prepared corpus is required}"
codec_collector_output="${3:?new output directory is required}"
codec_collector_study="${4:-original}"
case "${codec_collector_study}" in original|isolated) ;; *) echo 'Study must be original or isolated.' >&2; exit 1 ;; esac
codec_env_names="$(compgen -e)"
while IFS= read -r codec_env_name; do
  case "${codec_env_name}" in GO*|CGO*|MICROFAT*|SOURCE_DATE_EPOCH) unset -v "${codec_env_name}" ;; esac
done <<< "${codec_env_names}"
mkdir -p .work
if [[ "${codec_collector_study}" == isolated ]]; then
  codec_collector_output="$(realpath -m -- "${codec_collector_output}")"
  if [[ -e "${codec_collector_output}" || -L "${codec_collector_output}" ]]; then
    echo 'Output must be new; prior evidence is never overwritten.' >&2
    exit 1
  fi
  codec_bootstrap_config="${codec_collector_output}.bootstrap-config"
  umask 077
  mkdir -m 700 -- "${codec_bootstrap_config}"
  mkdir -p -- "${codec_bootstrap_config}/go/telemetry"
  printf 'off 1970-01-01\n' > "${codec_bootstrap_config}/go/telemetry/mode"
  export XDG_CONFIG_HOME="${codec_bootstrap_config}"
fi
export GOENV=off GOWORK=off GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOMAXPROCS=4
codec_collector_go_root="$("${codec_collector_go}" env GOROOT)"
export PATH="${codec_collector_go_root}/bin:${PATH}"
codec_collector_arch="$("${codec_collector_go}" env GOHOSTARCH)"
export GOARCH="${codec_collector_arch}"
if [[ "${codec_collector_arch}" == arm64 ]]; then export GOARM64=v8.0; else export GOAMD64=v1; fi
timeout --kill-after=10s 600 "${codec_collector_go}" build -mod=readonly -trimpath -buildvcs=false \
  -ldflags='-s -w -buildid=' -o .work/codec-policy-collector ./tests/e2e/testdata/codecpolicy
exec .work/codec-policy-collector -corpus "${codec_collector_corpus}" -output "${codec_collector_output}" \
  -study "${codec_collector_study}"
