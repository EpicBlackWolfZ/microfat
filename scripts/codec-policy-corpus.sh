#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

# Build inputs outside all measurement windows. Never execute downloaded tools here.
corpus_root="${1:?usage: codec-policy-corpus.sh NEW_OUTPUT [amd64|arm64]}"
corpus_repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
corpus_lock="${corpus_repo}/experiments/codecpolicy/sources.lock.json"
corpus_generator="tests/e2e/testdata/codecboundary/main.go"
corpus_go="$(command -v "${GO:-go}")"
if [[ "${corpus_go}" != /* ]]; then corpus_go="${PWD}/${corpus_go}"; fi
command -v jq >/dev/null
command -v sha256sum >/dev/null
command -v timeout >/dev/null

# Ignore user GOENV settings and inherited build/runtime overrides. In particular,
# GOFLAGS, GOEXPERIMENT, GOAMD64/GOARM64 and MICROFAT_* must not change the recipe.
corpus_inherited_names="$(compgen -e)"
while IFS= read -r corpus_env_name; do
  case "${corpus_env_name}" in
    GO*|CGO*|MICROFAT*|SOURCE_DATE_EPOCH) unset -v "${corpus_env_name}" ;;
  esac
done <<< "${corpus_inherited_names}"
export GOENV=off GOWORK=off GOTOOLCHAIN=local GOMAXPROCS=4
export CGO_ENABLED=0 GOOS=linux LC_ALL=C TZ=UTC

corpus_go_version="$("${corpus_go}" env GOVERSION)"
if [[ "${corpus_go_version}" != go1.27.3 ]]; then
  echo 'Go 1.27.3 is required; do not change the project toolchain.' >&2
  exit 1
fi
corpus_go_arch="$("${corpus_go}" env GOHOSTARCH)"
corpus_go_os="$("${corpus_go}" env GOHOSTOS)"
corpus_arch="${2:-${corpus_go_arch}}"
if [[ "${corpus_arch}" != amd64 && "${corpus_arch}" != arm64 ]]; then
  echo 'Expected amd64 or arm64.' >&2
  exit 1
fi
if [[ "${corpus_go_arch}" != "${corpus_arch}" || "${corpus_go_os}" != linux ]]; then
  echo 'Build this corpus on its native Linux architecture.' >&2
  exit 1
fi
if [[ -e "${corpus_root}" || -L "${corpus_root}" ]]; then
  echo 'Output must be new; prior evidence is never overwritten.' >&2
  exit 1
fi
corpus_source_status="$(git -C "${corpus_repo}" status --porcelain=v1 --untracked-files=all)"
if [[ -n "${corpus_source_status}" ]]; then
  echo 'Corpus source must be clean and committed before building.' >&2
  exit 1
fi
mkdir -p -- "${corpus_root}"
corpus_root="$(cd -- "${corpus_root}" && pwd -P)"
corpus_revision="$(git -C "${corpus_repo}" rev-parse HEAD)"
git -C "${corpus_repo}" status --porcelain=v1 > "${corpus_root}/source-status.txt"
cp -- "${corpus_lock}" "${corpus_root}/sources.lock.json"
: > "${corpus_root}/variants.ndjson"
export GOARCH="${corpus_arch}"
corpus_levels=(v1 v2 v3)
if [[ "${corpus_arch}" == arm64 ]]; then corpus_levels=(v8.0 v8.1 v8.2); fi
corpus_go_digest="$(sha256sum -- "${corpus_go}")"
corpus_go_digest="${corpus_go_digest%% *}"
corpus_generator_digest="$(sha256sum -- "${corpus_repo}/${corpus_generator}")"
corpus_generator_digest="${corpus_generator_digest%% *}"
"${corpus_go}" env -json > "${corpus_root}/go-env.json"
uname -a > "${corpus_root}/host.txt"
corpus_host_kernel="$(uname -r)"
corpus_host_machine="$(uname -m)"

corpus_go_bounded() {
  timeout --signal=TERM --kill-after=10s 600s "${corpus_go}" "${@}"
}

corpus_record_variant() {
  local corpus_family="${1}" corpus_profile="${2}" corpus_level="${3}" corpus_binary="${4}"
  local corpus_digest corpus_bytes
  corpus_digest="$(sha256sum -- "${corpus_binary}")"
  corpus_digest="${corpus_digest%% *}"
  corpus_bytes="$(stat -c %s -- "${corpus_binary}")"
  jq -cn --arg family "${corpus_family}" --arg profile "${corpus_profile}" \
    --arg level "${corpus_level}" --arg path "${corpus_binary}" --arg sha256 "${corpus_digest}" \
    --argjson bytes "${corpus_bytes}" \
    '{family:$family,profile:$profile,level:$level,path:$path,sha256:$sha256,bytes:$bytes}' \
    >> "${corpus_root}/variants.ndjson"
}

for corpus_index in 0 1 2 3; do
  corpus_source="$(jq -ce ".sources[${corpus_index}]" "${corpus_lock}")"
  corpus_family="$(jq -er '.family' <<< "${corpus_source}")"
  corpus_module="$(jq -er '.module' <<< "${corpus_source}")"
  corpus_version="$(jq -er '.version' <<< "${corpus_source}")"
  corpus_package="$(jq -er '.package' <<< "${corpus_source}")"
  corpus_metadata="${corpus_root}/${corpus_family}.module.json"
  (
    cd -- "${corpus_root}"
    corpus_go_bounded mod download -json "${corpus_module}@${corpus_version}"
  ) > "${corpus_metadata}"
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
        corpus_go_bounded build -mod=readonly -trimpath -buildvcs=false -ldflags="${corpus_ldflags}" \
          -o "${corpus_binary}" "${corpus_package}"
      )
      "${corpus_go}" version -m "${corpus_binary}" > "${corpus_binary}.buildinfo"
      corpus_record_variant "${corpus_family}" "${corpus_profile}" "${corpus_level}" "${corpus_binary}"
    done
  done
done

if [[ "${corpus_arch}" == amd64 ]]; then export GOAMD64=v1; else export GOARM64=v8.0; fi
(
  cd -- "${corpus_repo}"
  corpus_go_bounded run -mod=readonly -trimpath -buildvcs=false \
    "./${corpus_generator}" -arch "${corpus_arch}" -output "${corpus_root}"
  corpus_go_bounded build -mod=readonly -trimpath -buildvcs=false -ldflags='-s -w -buildid=' \
    -o "${corpus_root}/stub-full" ./cmd/microfat-stub
  corpus_go_bounded build -mod=readonly -tags minimal -trimpath -buildvcs=false -ldflags='-s -w -buildid=' \
    -o "${corpus_root}/stub-minimal" ./cmd/microfat-stub
  corpus_go_bounded build -mod=readonly -trimpath -buildvcs=false -ldflags='-s -w -buildid=' \
    -o "${corpus_root}/microfat" ./cmd/microfat
)
for corpus_profile in below at above; do
  for corpus_level in "${corpus_levels[@]}"; do
    corpus_record_variant boundary "${corpus_profile}" "${corpus_level}" "${corpus_root}/boundary-${corpus_profile}"
  done
done
for corpus_tool in stub-full stub-minimal microfat; do
  "${corpus_go}" version -m "${corpus_root}/${corpus_tool}" > "${corpus_root}/${corpus_tool}.buildinfo"
done
git -C "${corpus_repo}" status --porcelain=v1 --untracked-files=all > "${corpus_root}/source-status-after.txt"
corpus_final_revision="$(git -C "${corpus_repo}" rev-parse HEAD)"
if [[ -s "${corpus_root}/source-status-after.txt" || "${corpus_final_revision}" != "${corpus_revision}" ]]; then
  echo 'Corpus source changed during building; partial outputs are retained and are not qualification evidence.' >&2
  exit 1
fi
jq -sn --arg revision "${corpus_revision}" --arg architecture "${corpus_arch}" \
  --arg go_version "${corpus_go_version}" --arg go_path "${corpus_go}" --arg go_sha256 "${corpus_go_digest}" \
  --arg generator "${corpus_generator}" --arg generator_sha256 "${corpus_generator_digest}" \
  --arg host_kernel "${corpus_host_kernel}" --arg host_machine "${corpus_host_machine}" \
  --slurpfile sources "${corpus_lock}" --slurpfile variants "${corpus_root}/variants.ndjson" '
  {schema_version:2,source_revision:$revision,go_version:$go_version,architecture:$architecture,
   sources:$sources[0].sources,
   synthetic_sources:[{family:"boundary",kind:"synthetic-static-linux-elf",generator:$generator,
     generator_sha256:$generator_sha256,readiness_args:[],marker:"microfat-codec-boundary\n",
     go_runtime:false,boundary_bytes:524288,profiles:{below:524287,at:524288,above:524289}}],
   host:{os:"linux",kernel:$host_kernel,machine:$host_machine},
   toolchain:{path:$go_path,sha256:$go_sha256,environment:"go-env.json"},
   build:{cgo_enabled:"0",goos:"linux",goenv:"off",gowork:"off",gotoolchain:"local",gomaxprocs:4,
     trimpath:true,buildvcs:false,mod:"readonly",stripped_ldflags:"-s -w -buildid=",unstripped_ldflags:"-buildid=",
     command_timeout_seconds:600,termination_grace_seconds:10},
   variants:$variants}' > "${corpus_root}/manifest.json"
(
  cd -- "${corpus_root}"
  sha256sum -- ./* > SHA256SUMS
)
echo "Corpus built outside timing: ${corpus_root}/manifest.json"
