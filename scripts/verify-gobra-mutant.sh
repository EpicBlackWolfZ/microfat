#!/usr/bin/env bash
# Shared mutant-sensitivity runner for pinned Gobra container verification.
set -euo pipefail
IFS=$'\n\t'

if [[ "$#" -ne 3 ]]; then
    echo "usage: verify-gobra-mutant.sh <label> <go_file> <sed_expr>" >&2
    exit 2
fi

label="$1"
go_file="$2"
sed_expr="$3"

if ! [[ "${go_file}" =~ ^[A-Za-z0-9._/-]+$ ]]; then
    echo "Invalid Gobra verification target path: ${go_file}" >&2
    exit 2
fi

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
if [[ ! -f "${go_file}" ]]; then
    echo "Missing Gobra verification target: ${go_file}" >&2
    exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "${tmp_dir}"' EXIT

mkdir -p -- "${tmp_dir}/$(dirname -- "${go_file}")" "${tmp_dir}/verification/gobra/stubs/math"
cp -- "${go_file}" "${tmp_dir}/${go_file}"
cp -- verification/gobra/stubs/math/limits.gobra "${tmp_dir}/verification/gobra/stubs/math/limits.gobra"
sed -i -- "${sed_expr}" "${tmp_dir}/${go_file}"

if cmp -s -- "${go_file}" "${tmp_dir}/${go_file}"; then
    echo "Gobra mutant expression did not modify ${go_file}: ${sed_expr}" >&2
    exit 1
fi

image="ghcr.io/viperproject/gobra@sha256:${GOBRA_LINUX_AMD64#*@}"
printf 'Checking %s mutant sensitivity with Gobra %s (%s)\n' "${label}" "${GOBRA_LINUX_AMD64%@*}" "${image}"

container_cmd='tar -xf - -C /proof && exec java -Xss128m -jar /gobra/gobra.jar --backend SILICON --overflow --checkConsistency --noassumeInjectivityOnInhale --packageTimeout 1m -I /proof/verification/gobra/stubs -i "/proof/'
container_cmd+="${go_file}\""

if output="$(
    tar -C "${tmp_dir}" -cf - "${go_file}" verification/gobra/stubs/math/limits.gobra |
        "${engine}" run --rm --interactive --platform=linux/amd64 --network=none --read-only \
            --cap-drop=ALL --security-opt=no-new-privileges --user=65534:65534 \
            --tmpfs=/proof:rw,nosuid,nodev,noexec,mode=1777,size=4m --entrypoint=/bin/sh "${image}" -ec \
            "${container_cmd}" 2>&1
)"; then
    printf '%s\n' "${output}" >&2
    echo "Gobra mutant sensitivity check unexpectedly passed for ${label}!" >&2
    exit 1
fi
if grep -Fq ' - Gobra found 0 errors.' <<< "${output}"; then
    printf '%s\n' "${output}" >&2
    echo "Gobra reported 0 errors on mutant for ${label}!" >&2
    exit 1
fi
printf 'Gobra rejected mutant for %s as expected.\n' "${label}"
