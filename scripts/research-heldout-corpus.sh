#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

# Build inputs outside all measurement windows. Never execute downloaded tools here.
corpus_go="${GO:-go}"
corpus_root="${1:?usage: research-heldout-corpus.sh NEW_OUTPUT [amd64|arm64]}"
corpus_arch="${2:-amd64}"
corpus_repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
corpus_lock="${corpus_repo}/experiments/heldoutcorpus/sources.lock.json"
command -v jq >/dev/null
command -v sha256sum >/dev/null
corpus_go_version="$("${corpus_go}" env GOVERSION)"
if [[ "${corpus_go_version}" != go1.27.3 ]]; then
  echo 'Go 1.27.3 is required; do not change the project toolchain.' >&2
  exit 1
fi
if [[ "${corpus_arch}" != amd64 && "${corpus_arch}" != arm64 ]]; then
  echo 'Expected amd64 or arm64.' >&2
  exit 1
fi
corpus_go_arch="$("${corpus_go}" env GOHOSTARCH)"
if [[ "${corpus_go_arch}" != "${corpus_arch}" ]]; then
  echo 'Build this corpus on its native architecture.' >&2
  exit 1
fi
if [[ -e "${corpus_root}" || -L "${corpus_root}" ]]; then
  echo 'Output must be new; prior evidence is never overwritten.' >&2
  exit 1
fi
mkdir -p -- "${corpus_root}"
corpus_root="$(cd -- "${corpus_root}" && pwd -P)"
corpus_revision="$(git -C "${corpus_repo}" rev-parse HEAD)"
cp -- "${corpus_lock}" "${corpus_root}/sources.lock.json"
: > "${corpus_root}/variants.ndjson"
corpus_levels=(v1 v2 v3)
if [[ "${corpus_arch}" == arm64 ]]; then corpus_levels=(v8.0 v8.1 v8.2); fi
export CGO_ENABLED=0 GOOS=linux GOARCH="${corpus_arch}" GOTOOLCHAIN=local
for corpus_index in 0 1 2 3; do
  corpus_source="$(jq -ce ".sources[${corpus_index}]" "${corpus_lock}")"
  corpus_family="$(jq -er '.family' <<< "${corpus_source}")"
  corpus_module="$(jq -er '.module' <<< "${corpus_source}")"
  corpus_version="$(jq -er '.version' <<< "${corpus_source}")"
  corpus_package="$(jq -er '.package' <<< "${corpus_source}")"
  corpus_metadata="${corpus_root}/${corpus_family}.module.json"
  "${corpus_go}" mod download -json "${corpus_module}@${corpus_version}" > "${corpus_metadata}"
  jq -e --argjson expected "${corpus_source}" '
    .Path == $expected.module and .Version == $expected.version and
    .Sum == $expected.module_sum and .GoModSum == $expected.go_mod_sum and
    .Origin.Hash == $expected.source_commit' "${corpus_metadata}" >/dev/null
  corpus_module_dir="$(jq -er '.Dir' "${corpus_metadata}")"
  for corpus_profile in stripped unstripped; do
    corpus_ldflags='-buildid='
    if [[ "${corpus_profile}" == stripped ]]; then corpus_ldflags='-s -w -buildid='; fi
    for corpus_level in "${corpus_levels[@]}"; do
      if [[ "${corpus_arch}" == amd64 ]]; then
        export GOAMD64="${corpus_level}"
      else
        export GOARM64="${corpus_level}"
      fi
      corpus_binary="${corpus_root}/${corpus_family}-${corpus_profile}-${corpus_level}"
      (
        cd -- "${corpus_module_dir}"
        "${corpus_go}" build -mod=readonly -trimpath -buildvcs=false -ldflags="${corpus_ldflags}" \
          -o "${corpus_binary}" "${corpus_package}"
      )
      corpus_digest="$(sha256sum -- "${corpus_binary}")"
      corpus_digest="${corpus_digest%% *}"
      corpus_bytes="$(stat -c %s -- "${corpus_binary}")"
      "${corpus_go}" version -m "${corpus_binary}" > "${corpus_binary}.buildinfo"
      jq -cn --arg family "${corpus_family}" --arg profile "${corpus_profile}" \
        --arg level "${corpus_level}" --arg path "${corpus_binary}" --arg sha256 "${corpus_digest}" \
        --argjson bytes "${corpus_bytes}" \
        '{family:$family,profile:$profile,level:$level,path:$path,sha256:$sha256,bytes:$bytes}' \
        >> "${corpus_root}/variants.ndjson"
    done
  done
done
if [[ "${corpus_arch}" == amd64 ]]; then export GOAMD64=v1; else export GOARM64=v8.0; fi
(
  cd -- "${corpus_repo}"
  "${corpus_go}" build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' \
    -o "${corpus_root}/stub-full" ./cmd/microfat-stub
  "${corpus_go}" build -tags minimal -trimpath -buildvcs=false -ldflags='-s -w -buildid=' \
    -o "${corpus_root}/stub-minimal" ./cmd/microfat-stub
  "${corpus_go}" build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' \
    -o "${corpus_root}/microfat" ./cmd/microfat
)
jq -sn --arg revision "${corpus_revision}" --arg architecture "${corpus_arch}" \
  --slurpfile sources "${corpus_lock}" --slurpfile variants "${corpus_root}/variants.ndjson" '
  {schema_version:2,source_revision:$revision,go_version:"go1.27.3",architecture:$architecture,
   sources:$sources[0].sources,build:{cgo_enabled:"0",goos:"linux",trimpath:true,buildvcs:false},variants:$variants}' \
  > "${corpus_root}/manifest.json"
sha256sum -- "${corpus_root}/manifest.json" "${corpus_root}/stub-full" \
  "${corpus_root}/stub-minimal" "${corpus_root}/microfat" > "${corpus_root}/SHA256SUMS"
echo "Corpus built outside timing: ${corpus_root}/manifest.json"
