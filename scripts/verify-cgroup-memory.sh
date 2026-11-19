#!/usr/bin/env bash
# Check the cgroup memory laws against the same source file built by Go.
set -euo pipefail
IFS=$'\n\t'
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
# shellcheck source=../.config/tool-versions.env
source .config/tool-versions.env

engine="${GOBRA_ENGINE:-docker}"
case "${engine}" in
    docker|podman) ;;
    *) echo 'GOBRA_ENGINE must be docker or podman' >&2; exit 1 ;;
esac
command -v "${engine}" >/dev/null || { echo "Required container engine not found: ${engine}" >&2; exit 1; }
if ! [[ "${GOBRA_LINUX_AMD64:-}" =~ ^v[0-9]+\.[0-9]+@[0-9a-f]{64}$ ]]; then
    echo 'GOBRA_LINUX_AMD64 must pin a release and OCI manifest digest' >&2
    exit 1
fi
image="ghcr.io/viperproject/gobra@sha256:${GOBRA_LINUX_AMD64#*@}"
printf 'Checking cgroup memory with Gobra %s (%s)\n' "${GOBRA_LINUX_AMD64%@*}" "${image}"

# Stream only the proof inputs: no checkout mount, credentials or network in the verifier.
if ! output="$(
    tar -cf - internal/cgroup/memory.go verification/gobra/stubs/math/limits.gobra |
        "${engine}" run --rm --interactive --platform=linux/amd64 --network=none --read-only \
            --cap-drop=ALL --security-opt=no-new-privileges --user=65534:65534 \
            --tmpfs=/proof:rw,nosuid,nodev,noexec,mode=1777,size=4m --entrypoint=/bin/sh "${image}" -ec '
                tar -xf - -C /proof
                exec java -Xss128m -jar /gobra/gobra.jar --backend SILICON --overflow --checkConsistency \
                    --noassumeInjectivityOnInhale --packageTimeout 1m -I /proof/verification/gobra/stubs \
                    -i /proof/internal/cgroup/memory.go
            ' 2>&1
)"; then
    printf '%s\n' "${output}" >&2
    exit 1
fi
printf '%s\n' "${output}"
if ! grep -Fq ' - Gobra found 0 errors.' <<< "${output}"; then
    echo 'Gobra did not report a successful verification' >&2
    exit 1
fi
