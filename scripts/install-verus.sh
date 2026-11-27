#!/usr/bin/env bash
# Install pinned Verus verifier for Linux x86-64 or arm64.
set -euo pipefail
IFS=$'\n\t'

# shellcheck source=../.config/tool-versions.env
source "$(dirname -- "${BASH_SOURCE[0]}")/../.config/tool-versions.env"
: "${VERUS_LINUX_AMD64:?missing shared Verus amd64 pin}" "${VERUS_LINUX_ARM64:?missing shared Verus arm64 pin}"

verus_dest="${1:?usage: install-verus.sh DESTINATION_DIRECTORY}"
case "$(uname -sm)" in
    'Linux x86_64')
        verus_arch='x86'
        verus_pin="${VERUS_LINUX_AMD64}"
        ;;
    'Linux aarch64')
        verus_arch='arm64'
        verus_pin="${VERUS_LINUX_ARM64}"
        ;;
    *)
        echo 'This pinned Verus installer requires Linux x86_64 or aarch64' >&2
        exit 1
        ;;
esac

verus_tag="${verus_pin%@*}"
verus_ver="${verus_tag#v}"
verus_sha="${verus_pin#*@}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "${tmp_dir}"' EXIT

curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.3 --max-time 300 \
    "https://github.com/verus-lang/verus/releases/download/release/${verus_ver}/verus-${verus_ver}-${verus_arch}-linux.zip" \
    -o "${tmp_dir}/verus.zip"
printf '%s  %s\n' "${verus_sha}" "${tmp_dir}/verus.zip" | sha256sum --check --status

mkdir -p -- "${verus_dest}"
dest_abs="$(cd -- "${verus_dest}" && pwd)"
rm -rf -- "${dest_abs}/verus-dist"
mkdir -p -- "${dest_abs}/verus-dist"
unzip -q "${tmp_dir}/verus.zip" -d "${dest_abs}/verus-dist"

verus_bin="${dest_abs}/verus-dist/verus-${verus_arch}-linux/verus"
if [[ ! -x "${verus_bin}" ]]; then
    echo "Expected Verus binary not found at ${verus_bin}" >&2
    exit 1
fi

cat > "${dest_abs}/verus" <<EOF
#!/usr/bin/env bash
set -euo pipefail
exec "${verus_bin}" "\$@"
EOF
chmod 0755 "${dest_abs}/verus"
"${dest_abs}/verus" --version
