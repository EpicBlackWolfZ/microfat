#!/usr/bin/env bash
# Install the reviewed standalone cdxgen converter; no Python, BLINT or Node setup.
set -euo pipefail
IFS=$'\n\t'
# shellcheck source=.config/tool-versions.env
source "$(dirname -- "${BASH_SOURCE[0]}")/../.config/tool-versions.env"
CDXGEN_DEST="${1:?usage: install-cdxgen.sh DESTINATION_DIRECTORY}"
case "$(uname -sm)" in
    'Linux x86_64')
        CDXGEN_ARCH=amd64
        CDXGEN_PIN=${CDXGEN_LINUX_AMD64}
        ;;
    'Linux aarch64')
        CDXGEN_ARCH=arm64
        CDXGEN_PIN=${CDXGEN_LINUX_ARM64}
        ;;
    *) echo 'Release SBOM tools support Linux amd64 and arm64.' >&2; exit 1 ;;
esac
CDXGEN_VERSION=${CDXGEN_PIN%@*}
CDXGEN_SHA=${CDXGEN_PIN#*@}
CDXGEN_TEMP="$(mktemp -d)"
trap 'rm -rf "${CDXGEN_TEMP}"' EXIT
curl --fail --show-error --silent --location --connect-timeout 15 --max-time 180 \
    "https://github.com/cdxgen/cdxgen/releases/download/${CDXGEN_VERSION}/cdx-convert-linux-${CDXGEN_ARCH}" \
    --output "${CDXGEN_TEMP}/cdx-convert"
printf '%s  %s\n' "${CDXGEN_SHA}" "${CDXGEN_TEMP}/cdx-convert" | sha256sum --check --status
mkdir -p "${CDXGEN_DEST}"
install -m 0755 "${CDXGEN_TEMP}/cdx-convert" "${CDXGEN_DEST}/cdx-convert"
printf 'Installed cdxgen cdx-convert %s (%s) with pinned SHA-256.\n' "${CDXGEN_VERSION}" "${CDXGEN_ARCH}"
