#!/usr/bin/env bash
# This bootstrap is pinned to the first helper release, independently of the
# product --version. It becomes usable once those signed assets are published.
# All effects are inside a function invoked only after its complete definition.
microfat_bootstrap() (
    set -euo pipefail
    IFS=$'\n\t'

    readonly helper_version='0.3.0'
    readonly cosign_version='v3.0.6'
    readonly repository='https://github.com/EpicBlackWolfZ/microfat'
    readonly issuer='https://token.actions.githubusercontent.com'
    readonly metadata_limit=1048576
    readonly executable_limit=268435456
    local arch cosign='' pin='' staging_parent='' stage='' arg checksum_line expected='' actual asset verifier_info owner mode links parent
    local -a forwarded=()

    fail() { printf 'microfat bootstrap: %s\n' "$*" >&2; exit 1; }
    download() {
        curl --fail --silent --show-error --location --max-redirs 5 \
            --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 180 \
            --max-filesize "$3" --output "$2" "$1"
    }
    digest() {
        local value
        value=$(sha256sum < "$1")
        printf '%s' "${value%% *}"
    }
    safe_staging_parent() {
        local parent=$1 info owner mode
        [[ ${parent} == /* ]] || fail 'staging parent must be absolute'
        case "${parent}" in
            */../*|*/..|*/./*|*/.|*//*) fail 'staging parent must be a clean absolute directory' ;;
        esac
        while :; do
            [[ -d ${parent} && ! -L ${parent} ]] || fail "unsafe staging ancestor: ${parent}"
            info=$(stat -c '%u %a' -- "${parent}")
            IFS=' ' read -r owner mode <<< "${info}"
            [[ ${owner} == "${EUID}" || ${owner} == 0 ]] || fail "untrusted staging ancestor owner: ${parent}"
            if (( (8#${mode} & 0022) != 0 )); then
                if [[ ${owner} != 0 ]] || (( (8#${mode} & 01000) == 0 )); then
                    fail "writable staging ancestor: ${parent}"
                fi
            fi
            [[ ${parent} != / ]] || break
            parent=${parent%/*}
            if [[ -z ${parent} ]]; then parent=/; fi
        done
    }

    while (( $# > 0 )); do
        arg=$1
        shift
        case "${arg}" in
            --help|-h)
                printf '%s\n' 'Verified microfat installer (Linux amd64/arm64)' \
                    '  --version VERSION       Exact stable product release (default: latest)' \
                    '  --bin-dir PATH          Entrypoints (default: ~/.local/bin)' \
                    '  --store-dir PATH        Generation store (default: XDG user data)' \
                    '  --staging-dir PATH      Absolute executable temporary directory parent' \
                    '  --cosign PATH --cosign-sha256 HEX   Independently pinned verifier override' \
                    '  --system                Root only; requires explicit bin/store directories' \
                    '  --allow-downgrade       Requires explicit --version' \
                    '  --repair                Repair an owned corrupt installation' \
                    '  --uninstall             Remove owned links, retain generations and user data'
                exit 0
                ;;
            --cosign|--cosign-sha256|--staging-dir)
                (( $# > 0 )) || fail "missing value for ${arg}"
                case "${arg}" in
                    --cosign) cosign=$1 ;;
                    --cosign-sha256) pin=$1 ;;
                    --staging-dir) staging_parent=$1 ;;
                esac
                shift
                ;;
            --cosign=*) cosign=${arg#*=} ;;
            --cosign-sha256=*) pin=${arg#*=} ;;
            --staging-dir=*) staging_parent=${arg#*=} ;;
            *) forwarded+=("${arg}") ;;
        esac
    done

    for arg in uname curl mktemp sha256sum chmod rm stat; do
        command -v "${arg}" >/dev/null 2>&1 || fail "required tool not found: ${arg}"
    done
    arg=$(uname -s)
    [[ ${arg} == Linux ]] || fail 'supported systems are Linux amd64 and arm64'
    arg=$(uname -m)
    case "${arg}" in
        x86_64) arch=amd64 ;;
        aarch64|arm64) arch=arm64 ;;
        *) fail 'supported systems are Linux amd64 and arm64' ;;
    esac
    if [[ -n ${cosign} || -n ${pin} ]]; then
        [[ ${cosign} == /* && -f ${cosign} && ! -L ${cosign} && ${pin} =~ ^[0-9a-f]{64}$ ]] ||
            fail 'verifier override requires an absolute regular file and independent lowercase SHA-256 pin'
    elif [[ ${arch} == amd64 ]]; then
        pin=c956e5dfcac53d52bcf058360d579472f0c1d2d9b69f55209e256fe7783f4c74
    else
        pin=bedac92e8c3729864e13d4a17048007cfafa79d5deca993a43a90ffe018ef2b8
    fi
    # Pins are taken from sigstore/cosign-installer action.yml at the reviewed
    # immutable revision 6f9f17788090df1f26f669e9d70d6ae9567deba6.
    if [[ -n ${staging_parent} ]]; then
        safe_staging_parent "${staging_parent}"
        stage=$(mktemp -d "${staging_parent}/microfat-bootstrap.XXXXXXXXXX")
        forwarded+=(--staging-dir "${staging_parent}")
    else
        safe_staging_parent "${TMPDIR:-/tmp}"
        stage=$(mktemp -d)
    fi
    trap 'rm -rf -- "${stage}"' EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    chmod 700 "${stage}"
    if [[ -z ${cosign} ]]; then
        cosign="${stage}/cosign"
        download "https://github.com/sigstore/cosign/releases/download/${cosign_version}/cosign-linux-${arch}" \
            "${cosign}" "${executable_limit}"
        actual=$(digest "${cosign}")
        [[ ${actual} == "${pin}" ]] || fail 'Cosign checksum mismatch; verifier was not executed'
        chmod 700 "${cosign}"
    else
        parent=${cosign%/*}
        safe_staging_parent "${parent:-/}"
        verifier_info=$(stat -c '%u %a %h' -- "${cosign}")
        IFS=' ' read -r owner mode links <<< "${verifier_info}"
        [[ ${owner} == "${EUID}" || ${owner} == 0 ]] || fail 'untrusted verifier owner'
        (( (8#${mode} & 06022) == 0 && links == 1 )) || fail 'unsafe verifier permissions or hardlinks'
        actual=$(digest "${cosign}")
        [[ ${actual} == "${pin}" ]] || fail 'Cosign checksum mismatch; verifier was not executed'
        [[ -x ${cosign} ]] || fail 'independently supplied verifier is not executable'
    fi
    asset="microfat-install_${helper_version}_linux_${arch}"
    download "${repository}/releases/download/v${helper_version}/checksums.txt" "${stage}/checksums.txt" "${metadata_limit}"
    download "${repository}/releases/download/v${helper_version}/checksums.txt.sig" "${stage}/checksums.txt.sig" "${metadata_limit}"
    # Ambient verifier options must not change the fixed publisher policy.
    for arg in ${!COSIGN_@} ${!SIGSTORE_@}; do unset "${arg}"; done
    "${cosign}" verify-blob --certificate-identity "${repository}/.github/workflows/release.yml@refs/tags/v${helper_version}" \
        --certificate-oidc-issuer "${issuer}" --bundle "${stage}/checksums.txt.sig" "${stage}/checksums.txt" ||
        fail 'helper signature verification failed (staging must permit execution; use --staging-dir if necessary)'
    local checksum_pattern='^([[:xdigit:]]{64}) [ *](.+)$'
    while IFS= read -r checksum_line || [[ -n ${checksum_line} ]]; do
        [[ ${checksum_line} =~ ${checksum_pattern} ]] || fail 'invalid signed checksum record'
        if [[ ${BASH_REMATCH[2]} == "${asset}" ]]; then
            [[ -z ${expected} ]] || fail 'duplicate installer helper checksum'
            expected=${BASH_REMATCH[1],,}
        fi
    done < "${stage}/checksums.txt"
    [[ -n ${expected} ]] || fail 'installer helper is missing from signed checksums'
    download "${repository}/releases/download/v${helper_version}/${asset}" "${stage}/helper" "${executable_limit}"
    actual=$(digest "${stage}/helper")
    [[ ${actual} == "${expected}" ]] || fail 'installer helper checksum mismatch; helper was not executed'
    chmod 700 "${stage}/helper"
    local status=0
    "${stage}/helper" --cosign "${cosign}" --cosign-sha256 "${pin}" "${forwarded[@]}" || status=$?
    if (( status == 126 )); then
        fail 'cannot execute authenticated helper; staging must permit execution (use --staging-dir)'
    fi
    exit "${status}"
)

microfat_bootstrap "$@"
