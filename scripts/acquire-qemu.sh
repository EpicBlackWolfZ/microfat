#!/usr/bin/env bash
# Extract authenticated Ubuntu package bytes without installing binfmt handlers.
set -euo pipefail
IFS=$'\n\t'

output=${1:?usage: acquire-qemu.sh output-directory}
mkdir -p -- "${output}"
output=$(cd -- "${output}" && pwd)
sudo -n apt-get -o Acquire::AllowInsecureRepositories=false -o APT::Get::AllowUnauthenticated=false update
version=$(apt-cache policy qemu-user-static | awk '$1 == "Candidate:" {print $2}')
if [[ -z "${version}" || "${version}" == '(none)' ]]; then
    printf 'No authenticated qemu-user-static candidate.\n' >&2
    exit 1
fi
apt-cache show "qemu-user-static=${version}" > "${output}/package-metadata.txt"
digest=$(awk '$1 == "SHA256:" {print $2; exit}' "${output}/package-metadata.txt")
if [[ ! "${digest}" =~ ^[a-f0-9]{64}$ ]]; then
    printf 'Missing package SHA-256 in authenticated APT metadata.\n' >&2
    exit 1
fi
download=$(mktemp -d "${output}/download-XXXXXX")
(
    cd -- "${download}"
    apt-get -o APT::Get::AllowUnauthenticated=false download "qemu-user-static=${version}"
    packages=(*.deb)
    if [[ ${#packages[@]} != 1 ]]; then
        printf 'Expected exactly one emulator package.\n' >&2
        exit 1
    fi
    printf '%s  %s\n' "${digest}" "${packages[0]}" | sha256sum --check --strict
    dpkg-deb --extract "${packages[0]}" extracted
    cp -- extracted/usr/bin/qemu-aarch64-static "${output}/qemu-aarch64-static"
)
{
    printf 'APT authenticated package qemu-user-static=%s\nPackage SHA-256: %s\n' "${version}" "${digest}"
    sha256sum "${output}/qemu-aarch64-static"
    "${output}/qemu-aarch64-static" --version
} > "${output}/provenance.txt"
