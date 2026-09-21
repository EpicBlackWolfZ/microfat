#!/usr/bin/env bash
# Install the reviewed standalone cdxgen converter; no Python, BLINT or Node setup.
set -euo pipefail
IFS=$'\n\t'
CDXGEN_VERSION=13.1.0
CDXGEN_DEST="${1:?usage: install-cdxgen.sh DESTINATION_DIRECTORY}"
case "$(uname -sm)" in
    'Linux x86_64')
        CDXGEN_ARCH=amd64
        CDXGEN_SHA=e87137134bf53346f6ea864a2c481c070177c0ab28c303a26d4e79065ae6d266
        ;;
    'Linux aarch64')
        CDXGEN_ARCH=arm64
        CDXGEN_SHA=4832f37aa1b4aac12b0c3d6ad446a3b9e6324d51ae1921b90c670f494d47dfc9
        ;;
    *) echo 'Release SBOM tools support Linux amd64 and arm64.' >&2; exit 1 ;;
esac
CDXGEN_TEMP="$(mktemp -d)"
trap 'rm -rf "${CDXGEN_TEMP}"' EXIT
curl --fail --show-error --silent --location --connect-timeout 15 --max-time 180 \
    "https://github.com/cdxgen/cdxgen/releases/download/v${CDXGEN_VERSION}/cdx-convert-linux-${CDXGEN_ARCH}" \
    --output "${CDXGEN_TEMP}/cdx-convert"
printf '%s  %s\n' "${CDXGEN_SHA}" "${CDXGEN_TEMP}/cdx-convert" | sha256sum --check --status
mkdir -p "${CDXGEN_DEST}"
install -m 0755 "${CDXGEN_TEMP}/cdx-convert" "${CDXGEN_DEST}/cdx-convert"
printf 'Installed cdxgen cdx-convert v%s (%s) with pinned SHA-256.\n' "${CDXGEN_VERSION}" "${CDXGEN_ARCH}"
