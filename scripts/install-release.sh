#!/usr/bin/env bash
# Compatibility entrypoint. Installation is delegated to the verified bootstrap.
set -euo pipefail
IFS=$'\n\t'

if (( $# < 2 )); then
    printf '%s\n' 'Usage: install-release.sh VERSION ARCH [BIN_DIR] [installer flags...]' \
        'Prefer scripts/install.sh. User paths are now the default; system installs require explicit flags.' >&2
    exit 2
fi
if [[ -n ${MICROFAT_RELEASE_URL:-} || -n ${INSTALL_RUNNER:-} ]]; then
    printf '%s\n' 'Release URL and install-runner overrides are no longer supported; use the official verified installer.' >&2
    exit 2
fi
version=${1#v}
arch=$2
shift 2
if [[ ! ${version} =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    printf 'Invalid stable version: %s\n' "${version}" >&2
    exit 2
fi
host=$(uname -m)
case "${host}:${arch}" in
    x86_64:amd64|aarch64:arm64|arm64:arm64) ;;
    *) printf 'Requested architecture must match the native Linux host.\n' >&2; exit 2 ;;
esac
args=(--version "${version}")
if (( $# > 0 )) && [[ $1 != --* ]]; then
    args+=(--bin-dir "$1")
    shift
fi
if [[ -n ${MICROFAT_COSIGN:-} || -n ${MICROFAT_COSIGN_SHA256:-} ]]; then
    args+=(--cosign "${MICROFAT_COSIGN:-}" --cosign-sha256 "${MICROFAT_COSIGN_SHA256:-}")
fi
script_dir=${BASH_SOURCE[0]%/*}
if [[ ${script_dir} == "${BASH_SOURCE[0]}" ]]; then script_dir=.; fi
printf '%s\n' 'Delegating to the verified installer. Existing manual files must be moved aside; sudo is never invoked.' >&2
exec bash "${script_dir}/install.sh" "${args[@]}" "$@"
