#!/usr/bin/env bash
# Exercise a trusted, already-built or authenticated helper on a native host.
# Optional VERSION RELEASE_DIR adds a signed draft; no release assets are changed.
set -euo pipefail
IFS=$'\n\t'

helper=${1:?absolute trusted helper path required}
output=${2:?absolute evidence directory required}
candidate=${3:-}
release_dir=${4:-}
[[ ${helper} == /* && -f ${helper} && -x ${helper} && ${output} == /* && ${output} != / ]]
[[ -z ${candidate} && -z ${release_dir} || -n ${candidate} && ${release_dir} == /* ]]
(( EUID != 0 )) || { printf 'Run qualification as an ordinary user.\n' >&2; exit 1; }
mkdir -p "${output}"
private=$(mktemp -d "${output}/fixture.XXXXXXXXXX")
trap 'rm -rf -- "${private}"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
host=$(uname -m)
case "${host}" in
    x86_64) arch=amd64; pin=c956e5dfcac53d52bcf058360d579472f0c1d2d9b69f55209e256fe7783f4c74 ;;
    aarch64|arm64) arch=arm64; pin=bedac92e8c3729864e13d4a17048007cfafa79d5deca993a43a90ffe018ef2b8 ;;
    *) printf 'Native Linux amd64 or arm64 required.\n' >&2; exit 1 ;;
esac
cosign="${private}/cosign"
curl --fail --silent --show-error --location --max-redirs 5 --proto '=https' --proto-redir '=https' \
    --connect-timeout 15 --max-time 180 --max-filesize 268435456 \
    --output "${cosign}" "https://github.com/sigstore/cosign/releases/download/v3.0.6/cosign-linux-${arch}"
actual=$(sha256sum < "${cosign}")
[[ ${actual%% *} == "${pin}" ]]
chmod 700 "${cosign}"
fixture_home="${private}/home space"
mkdir "${fixture_home}"
common=(--cosign "${cosign}" --cosign-sha256 "${pin}" --staging-dir "${private}")
versions=(0.2.3 0.2.4 0.2.5)
if [[ -n ${candidate} ]]; then versions+=("${candidate}"); fi
for version in "${versions[@]}"; do
    args=(--version "${version}")
    if [[ -n ${candidate} && ${version} == "${candidate}" ]]; then args+=(--release-dir "${release_dir}"); fi
    # No Go, jq, Python, archive program or PATH Cosign is available to the
    # native helper. It may execute only its explicitly authenticated verifier.
    PATH=/nonexistent HOME="${fixture_home}" XDG_DATA_HOME='' XDG_CACHE_HOME="${private}/cache" \
        "${helper}" "${common[@]}" "${args[@]}"
    PATH=/nonexistent HOME="${fixture_home}" XDG_DATA_HOME='' XDG_CACHE_HOME="${private}/cache" \
        "${helper}" "${common[@]}" "${args[@]}"
    bin="${fixture_home}/.local/bin"
    for mode in memfd cache; do
        PATH=/nonexistent HOME="${fixture_home}" XDG_CACHE_HOME="${private}/cache" MICROFAT_EXEC_MODE="${mode}" \
            "${bin}/microfat" detect --json > "${output}/${version}-${mode}.json"
        for profile in full minimal; do
            # A trusted fixture helper is also a valid baseline Go ELF payload.
            tier=v1
            if [[ ${arch} == arm64 ]]; then tier=v8.0; fi
            stub_args=(--stub "${bin}/microfat-stub")
            if [[ ${profile} == minimal ]]; then stub_args=(--stub "${bin}/microfat-stub-minimal"); fi
            if [[ -n ${candidate} && ${version} == "${candidate}" ]]; then stub_args=(--stub-profile "${profile}"); fi
            PATH=/nonexistent HOME="${fixture_home}" XDG_CACHE_HOME="${private}/cache" MICROFAT_EXEC_MODE="${mode}" \
                "${bin}/microfat" pack --arch "${arch}" --name installer-qualification "${stub_args[@]}" \
                -v "${tier}=${helper}" -o "${private}/packed-${mode}-${profile}"
            PATH=/nonexistent HOME="${fixture_home}" XDG_CACHE_HOME="${private}/cache" MICROFAT_EXEC_MODE="${mode}" \
                "${private}/packed-${mode}-${profile}" --helper-version
        done
    done
    if [[ -n ${candidate} && ${version} == "${candidate}" ]]; then
        # Execute the updater inside externally authenticated candidate bytes.
        # Historical CLIs have no update command; restore with the trusted helper.
        for mode in memfd cache; do
            PATH=/nonexistent HOME="${fixture_home}" XDG_CACHE_HOME="${private}/cache" MICROFAT_EXEC_MODE="${mode}" \
                "${bin}/microfat" update --check --version 0.2.5 --json \
                > "${output}/updater-${mode}-check.json"
            PATH=/nonexistent HOME="${fixture_home}" XDG_CACHE_HOME="${private}/cache" MICROFAT_EXEC_MODE="${mode}" \
                "${bin}/microfat" update --version 0.2.5 --allow-downgrade \
                --cosign "${cosign}" --cosign-sha256 "${pin}" --staging-dir "${private}" --json \
                > "${output}/updater-${mode}-signed-downgrade.json"
            PATH=/nonexistent HOME="${fixture_home}" XDG_DATA_HOME='' "${helper}" "${common[@]}" "${args[@]}"
        done
    fi
done
PATH=/nonexistent HOME="${fixture_home}" XDG_DATA_HOME='' "${helper}" --uninstall
[[ ! -e "${fixture_home}/.local/bin/microfat" && ! -L "${fixture_home}/.local/bin/microfat" ]]
[[ -f "${fixture_home}/.local/share/microfat/installations/default/current/microfat" ]]
printf 'PASS native %s: authenticated install/reinstall, full/minimal packing, memfd/cache and conservative uninstall\n' "${arch}"
