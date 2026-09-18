#!/usr/bin/env bash
# Authoritative installation script for microfat release archives.
# Usage:
#   scripts/install-release.sh <version> <arch> [install_dir]
# Examples:
#   scripts/install-release.sh 0.2.3 amd64
#   scripts/install-release.sh 0.2.3 arm64 /usr/local/bin
set -euo pipefail
IFS=$'\n\t'

VERSION="${1:-${VERSION:-}}"
ARCH="${2:-${ARCH:-}}"
INSTALL_DIR="${3:-${INSTALL_DIR:-/usr/local/bin}}"

# 1. Validate version and architecture arguments
if [[ -z "${VERSION}" ]]; then
    echo "Error: Version argument required (e.g. 0.2.3)" >&2
    exit 1
fi
VERSION="${VERSION#v}" # strip leading 'v' if provided

if ! [[ "${VERSION}" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
    echo "Error: Invalid version format '${VERSION}'" >&2
    exit 1
fi

if [[ "${ARCH}" != "amd64" && "${ARCH}" != "arm64" ]]; then
    echo "Error: Unsupported architecture '${ARCH}' (expected 'amd64' or 'arm64')" >&2
    exit 1
fi

# 2. Setup private temporary work directory
WORK_DIR=$(mktemp -d 2>/dev/null || mktemp -d -t 'microfat-install.XXXXXX')
if [[ ! -d "${WORK_DIR}" ]]; then
    echo "Error: Failed to create temporary working directory" >&2
    exit 1
fi
trap 'rm -rf "${WORK_DIR}"' EXIT

ARCHIVE_NAME="microfat_${VERSION}_linux_${ARCH}.tar.gz"
RELEASE_URL="${MICROFAT_RELEASE_URL:-https://github.com/EpicBlackWolfZ/microfat/releases/download/v${VERSION}}"

echo "==> Downloading microfat v${VERSION} (${ARCH})..."

# 3. Download archive, checksums, and signature bundle using fail-on-error behavior
curl --fail -sSL -o "${WORK_DIR}/${ARCHIVE_NAME}" "${RELEASE_URL}/${ARCHIVE_NAME}" || {
    echo "Error: Failed to download release archive '${ARCHIVE_NAME}'" >&2
    exit 1
}
curl --fail -sSL -o "${WORK_DIR}/checksums.txt" "${RELEASE_URL}/checksums.txt" || {
    echo "Error: Failed to download checksums.txt" >&2
    exit 1
}
curl --fail -sSL -o "${WORK_DIR}/checksums.txt.sig" "${RELEASE_URL}/checksums.txt.sig" || {
    echo "Error: Failed to download checksums.txt.sig signature bundle" >&2
    exit 1
}

# 4. Verify keyless Cosign signature against official release identity
echo "==> Verifying signature with cosign..."
if ! command -v cosign >/dev/null 2>&1; then
    echo "Error: cosign executable not found in PATH; required for signature verification" >&2
    exit 1
fi

cosign verify-blob \
    --bundle "${WORK_DIR}/checksums.txt.sig" \
    --certificate-identity "https://github.com/EpicBlackWolfZ/microfat/.github/workflows/release.yml@refs/tags/v${VERSION}" \
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
    "${WORK_DIR}/checksums.txt" || {
    echo "Error: Cosign signature verification failed for checksums.txt" >&2
    exit 1
}

# 5. Select exactly one matching checksum record using exact filename matching
ENTRY=$(awk -v target="${ARCHIVE_NAME}" '
    $2 == target || $2 == "*"target {
        print $1, $2
    }
' "${WORK_DIR}/checksums.txt")

if [[ -z "${ENTRY}" ]]; then
    echo "Error: No matching checksum entry found for '${ARCHIVE_NAME}' in checksums.txt" >&2
    exit 1
fi

MATCH_COUNT=$(printf '%s\n' "${ENTRY}" | grep -c . || true)
if [[ "${MATCH_COUNT}" -ne 1 ]]; then
    echo "Error: Multiple matching checksum entries found for '${ARCHIVE_NAME}' (${MATCH_COUNT} matches)" >&2
    exit 1
fi

EXPECTED_HASH=$(printf '%s\n' "${ENTRY}" | awk '{print $1}')
if ! [[ "${EXPECTED_HASH}" =~ ^[0-9a-fA-F]{64}$ ]]; then
    echo "Error: Invalid checksum format '${EXPECTED_HASH}' for '${ARCHIVE_NAME}'" >&2
    exit 1
fi

# 6. Verify archive SHA-256 digest
echo "==> Verifying archive SHA-256 checksum..."
(
    cd "${WORK_DIR}"
    printf '%s\n' "${ENTRY}" | sha256sum --check --status
) || {
    echo "Error: SHA-256 checksum verification failed for '${ARCHIVE_NAME}'" >&2
    exit 1
}

# 7. Unprivileged extraction into fresh directory
echo "==> Extracting ${ARCHIVE_NAME}..."
tar -xzf "${WORK_DIR}/${ARCHIVE_NAME}" -C "${WORK_DIR}" || {
    echo "Error: Failed to extract release archive" >&2
    exit 1
}

# 8. Verify required binaries exist before attempting installation
REQUIRED_BINARIES=("microfat" "microfat-stub" "microfat-stub-minimal")
for bin in "${REQUIRED_BINARIES[@]}"; do
    if [[ ! -f "${WORK_DIR}/${bin}" ]]; then
        echo "Error: Expected binary '${bin}' missing from extracted archive" >&2
        exit 1
    fi
done

# 9. Perform installation
echo "==> Installing binaries to ${INSTALL_DIR}..."
INSTALL_CMD=(install)
current_uid="$(id -u)"
if [[ -n "${INSTALL_RUNNER:-}" ]]; then
    IFS=' ' read -r -a INSTALL_CMD <<< "${INSTALL_RUNNER}"
elif [[ "${current_uid}" -ne 0 ]] && [[ ! -w "${INSTALL_DIR}" ]]; then
    INSTALL_CMD=(sudo install)
fi

mkdir -p "${INSTALL_DIR}" 2>/dev/null || {
    if [[ "${INSTALL_CMD[*]}" == "sudo install" ]]; then
        sudo mkdir -p "${INSTALL_DIR}"
    else
        echo "Error: Cannot create installation directory '${INSTALL_DIR}'" >&2
        exit 1
    fi
}

for bin in "${REQUIRED_BINARIES[@]}"; do
    "${INSTALL_CMD[@]}" -m 0755 "${WORK_DIR}/${bin}" "${INSTALL_DIR}/${bin}" || {
        echo "Error: Failed to install '${bin}' to '${INSTALL_DIR}'" >&2
        exit 1
    }
done

echo "==> Successfully installed microfat v${VERSION} to ${INSTALL_DIR}"
