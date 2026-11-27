#!/usr/bin/env bash
# Install pinned Kani Rust verifier and its matching rustup toolchain.
set -euo pipefail
IFS=$'\n\t'

# shellcheck source=../.config/tool-versions.env
source "$(dirname -- "${BASH_SOURCE[0]}")/../.config/tool-versions.env"
: "${KANI_LINUX_AMD64:?missing shared Kani pin}"

kani_dest="${1:?usage: install-kani.sh DESTINATION_DIRECTORY}"
host_platform="$(uname -sm)"
if [[ "${host_platform}" != 'Linux x86_64' ]]; then
    echo 'This pinned Kani installer requires Linux x86-64' >&2
    exit 1
fi

kani_tag="${KANI_LINUX_AMD64%@*}"
kani_ver="${kani_tag#v}"
kani_sha="${KANI_LINUX_AMD64#*@}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "${tmp_dir}"' EXIT

curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.3 --max-time 300 \
    "https://github.com/model-checking/kani/releases/download/kani-${kani_ver}/kani-${kani_ver}-x86_64-unknown-linux-gnu.tar.gz" \
    -o "${tmp_dir}/kani.tar.gz"
printf '%s  %s\n' "${kani_sha}" "${tmp_dir}/kani.tar.gz" | sha256sum --check --status

mkdir -p -- "${kani_dest}"
dest_abs="$(cd -- "${kani_dest}" && pwd)"
rm -rf -- "${dest_abs}/kani-dist"
mkdir -p -- "${dest_abs}/kani-dist"
tar -xzf "${tmp_dir}/kani.tar.gz" --strip-components=1 -C "${dest_abs}/kani-dist"

kani_toolchain="$(tr -d '\r\n' < "${dest_abs}/kani-dist/rust-toolchain-version")"
rustup toolchain install "${kani_toolchain}" --profile minimal
toolchain_root="${HOME}/.rustup/toolchains/${kani_toolchain}"

cat > "${dest_abs}/kani-driver" <<EOF
#!/usr/bin/env bash
set -euo pipefail
unset RUSTUP_TOOLCHAIN
export LD_LIBRARY_PATH="${toolchain_root}/lib\${LD_LIBRARY_PATH:+:\${LD_LIBRARY_PATH}}"
export PATH="${toolchain_root}/bin:${dest_abs}/kani-dist/bin:\${PATH}"
exec "${dest_abs}/kani-dist/bin/kani-driver" "\$@"
EOF
chmod 0755 "${dest_abs}/kani-driver"
"${dest_abs}/kani-driver" --version
