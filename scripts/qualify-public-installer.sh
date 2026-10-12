#!/usr/bin/env bash
# Qualify the actual public bootstrap in a fresh native container without host tools.
set -euo pipefail
IFS=$'\n\t'

fail() { printf 'public installer qualification: %s\n' "$*" >&2; exit 1; }

container_probe() {
    local tag=$1 source=$2 arch=$3 version=${1#v} host bin store current bootstrap actual expected product mode profile tier
    local evidence=/evidence helper=/home/qualification/public-helper output link metadata bootstrap_hash latest_generation other_generation
    [[ ${tag} =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ && ${source} =~ ^[0-9a-f]{40}$ ]]
    (( EUID != 0 )) || fail 'container must run as an ordinary user'
    host=$(uname -m)
    case "${host}:${arch}" in x86_64:amd64|aarch64:arm64|arm64:arm64) ;; *) fail 'native architecture mismatch' ;; esac
    [[ ${HOME} == /home/qualification ]] || fail 'unexpected disposable home'
    printf '{"status":"incomplete","tag":"%s","source":"%s","arch":"%s"}\n' \
        "${tag}" "${source}" "${arch}" > "${evidence}/summary.json"
    exec 3> "${evidence}/commands.log"
    BASH_XTRACEFD=3
    set -x
    for product in go gofmt cosign jq python python3; do
        if command -v "${product}" >/dev/null 2>&1; then fail "unexpected preinstalled tool: ${product}"; fi
    done
    id > "${evidence}/user.txt"
    uname -a > "${evidence}/kernel.txt"
    dpkg-query -W > "${evidence}/packages.txt"
    printf '%s\n' 'PASS: no Go, Cosign, jq or Python installed before bootstrap' > "${evidence}/prerequisites.txt"
    bin="${HOME}/.local/bin"
    store="${HOME}/.local/share/microfat/installations/default"
    mkdir -p "${bin}" "${HOME}/.cache/user-workloads"
    printf '%s\n' keep > "${bin}/unrelated-tool"
    printf '%s\n' keep > "${HOME}/workload.keep"
    printf '%s\n' keep > "${HOME}/.cache/user-workloads/keep"
    bootstrap="${HOME}/install.sh"
    curl --fail --silent --show-error --location --max-redirs 5 --proto '=https' --proto-redir '=https' \
        --connect-timeout 15 --max-time 180 --max-filesize 1048576 \
        --dump-header "${evidence}/bootstrap.headers" --write-out '%{url_effective}\n' \
        --output "${bootstrap}" \
        "https://raw.githubusercontent.com/EpicBlackWolfZ/microfat/${tag}/scripts/install.sh" > "${evidence}/bootstrap.url"
    actual=$(sha256sum < "${bootstrap}")
    read -r expected < /qualification-input/bootstrap.sha256
    [[ ${actual%% *} == "${expected}" ]] || fail 'public bootstrap differs from reviewed tagged source; it was not executed'
    cp "${bootstrap}" "${evidence}/install.sh"
    bootstrap_hash=${actual%% *}
    printf '%s\n' "${bootstrap_hash}" > "${evidence}/bootstrap.sha256"
    # No verifier override, locally staged release, source checkout or host binary is supplied.
    bash "${bootstrap}" | tee "${evidence}/install-latest.log"
    for output in latest reinstall pinned; do
        if [[ ${output} == reinstall ]]; then bash "${bootstrap}" | tee "${evidence}/reinstall-latest.log"; fi
        if [[ ${output} == pinned ]]; then
            bash "${bootstrap}" --version "${version}" | tee "${evidence}/install-pinned.log"
        fi
        current=$(readlink -e "${store}/current")
        [[ -d ${current} ]] || fail 'default user generation is missing'
        for product in microfat microfat-stub microfat-stub-minimal; do
            link=$(readlink -e "${bin}/${product}")
            metadata=$(stat -c '%u %a' -- "${current}/${product}")
            [[ -L ${bin}/${product} && ${link} == "${current}/${product}" ]]
            [[ ${metadata} == "${EUID} 755" ]]
        done
        (cd "${bin}" && sha256sum --check /qualification-input/products.sha256) > "${evidence}/${output}-hash-check.log"
        (cd "${bin}" && sha256sum microfat microfat-stub microfat-stub-minimal) > "${evidence}/${output}-hashes.txt"
        "${bin}/microfat" --version > "${evidence}/${output}-version.txt"
        read -r actual < "${evidence}/${output}-version.txt"
        [[ ${actual} == "Version: ${version} (Commit: ${source},"* ]] || fail 'installed version/source differs from qualified release'
        printf '%s\n' "${current}" > "${evidence}/${output}-generation.txt"
    done
    read -r latest_generation < "${evidence}/latest-generation.txt"
    for output in reinstall pinned; do
        read -r other_generation < "${evidence}/${output}-generation.txt"
        [[ ${latest_generation} == "${other_generation}" ]]
    done
    # An independently hash-checked public native helper is a baseline ELF payload for packing.
    curl --fail --silent --show-error --location --max-redirs 5 --proto '=https' --proto-redir '=https' \
        --connect-timeout 15 --max-time 180 --max-filesize 268435456 --output "${helper}" \
        "https://github.com/EpicBlackWolfZ/microfat/releases/download/${tag}/microfat-install_${version}_linux_${arch}"
    actual=$(sha256sum < "${helper}")
    read -r expected < /qualification-input/helper.sha256
    [[ ${actual%% *} == "${expected}" ]] || fail 'public helper digest mismatch; it was not executed'
    chmod 700 "${helper}"
    printf '%s\n' "${actual%% *}" > "${evidence}/helper.sha256"
    tier=v1
    if [[ ${arch} == arm64 ]]; then tier=v8.0; fi
    export PATH="${bin}:/usr/bin:/bin" MICROFAT_CACHE_DIR="${HOME}/.cache/microfat"
    for mode in memfd cache; do
        MICROFAT_EXEC_MODE="${mode}" microfat detect --json > "${evidence}/detect-${mode}.json"
        MICROFAT_EXEC_MODE="${mode}" microfat update --check --version "${version}" --json > "${evidence}/update-${mode}.json"
        for profile in full minimal; do
            MICROFAT_EXEC_MODE="${mode}" microfat pack --arch "${arch}" --name public-installer \
                --stub-profile "${profile}" -v "${tier}=${helper}" -o "${HOME}/packed-${mode}-${profile}" \
                > "${evidence}/pack-${mode}-${profile}.log" 2>&1
            MICROFAT_EXEC_MODE="${mode}" "${HOME}/packed-${mode}-${profile}" --helper-version \
                > "${evidence}/payload-${mode}-${profile}.txt"
            read -r actual < "${evidence}/payload-${mode}-${profile}.txt"
            [[ ${actual} == "microfat-install Version: ${version} (Commit: ${source},"* ]]
        done
    done
    printf '%s\n' keep > "${MICROFAT_CACHE_DIR}/unrelated-cache.keep"
    bash "${bootstrap}" --uninstall | tee "${evidence}/uninstall.log"
    for product in microfat microfat-stub microfat-stub-minimal; do
        [[ ! -e ${bin}/${product} && ! -L ${bin}/${product} && -f ${current}/${product} ]]
    done
    for product in "${bin}/unrelated-tool" "${HOME}/workload.keep" "${HOME}/.cache/user-workloads/keep" \
        "${MICROFAT_CACHE_DIR}/unrelated-cache.keep"; do
        read -r actual < "${product}"
        [[ ${actual} == keep ]]
    done
    printf '{"status":"pass","tag":"%s","source":"%s","arch":"%s","uid":%s,"bootstrap_sha256":"%s"}\n' \
        "${tag}" "${source}" "${arch}" "${EUID}" "${bootstrap_hash}" > "${evidence}/summary.json"
}

if [[ ${1:-} == --container ]]; then
    (( $# == 4 )) || fail 'container invocation requires tag, source and architecture'
    container_probe "$2" "$3" "$4"
    exit 0
fi

(( $# == 6 )) || fail 'usage: qualify-public-installer.sh TAG SOURCE ARCH VERIFICATION_JSON TAGGED_BOOTSTRAP NEW_OUTPUT'
tag=$1 source=$2 arch=$3 verification=$4 reviewed_bootstrap=$5 output=$6
[[ ${tag} =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ && ${source} =~ ^[0-9a-f]{40}$ ]] || fail 'exact stable tag/source required'
[[ ${arch} == amd64 || ${arch} == arm64 ]] || fail 'unsupported architecture'
(( EUID != 0 )) || fail 'run qualification as an ordinary user'
host_os=$(uname -s)
host_arch=$(uname -m)
case "${host_os}:${host_arch}:${arch}" in
    Linux:x86_64:amd64|Linux:aarch64:arm64|Linux:arm64:arm64) ;;
    *) fail 'native Linux architecture mismatch' ;;
esac
for input in "${verification}" "${reviewed_bootstrap}"; do
    [[ ${input} == /* && -f ${input} && ! -L ${input} ]] || fail 'regular absolute authentication inputs required'
done
[[ ${output} == /* && ${output} != / && ! -e ${output} && ! -L ${output} && -d ${output%/*} ]] ||
    fail 'output must be a new absolute directory with an existing parent'
case "${output}" in */../*|*/..|*/./*|*/.|*//*) fail 'output must be a clean absolute path' ;; esac
version=${tag#v}
helper_name="microfat-install_${version}_linux_${arch}"
jq -e --arg tag "${tag}" --arg source "${source}" --arg arch "${arch}" --arg helper "${helper_name}" '
    .tag == $tag and .source == $source and (.release_id | type == "number" and . > 0) and
    (.checksums_sha256 | test("^[0-9a-f]{64}$")) and
    (.assets[$helper] | test("^[0-9a-f]{64}$")) and
    (.products[$arch] | keys == ["microfat", "microfat-stub", "microfat-stub-minimal"]) and
    (.products[$arch] | all(.[]; test("^[0-9a-f]{64}$")))
' "${verification}" >/dev/null || fail 'authentication evidence does not match the requested public release'
mkdir -m 700 "${output}"
private=$(mktemp -d "${output}/input.XXXXXXXXXX")
container_id='' image_name="microfat-public-installer:${private##*/}"
cleanup() {
    local status=$?
    trap - EXIT
    if [[ -n ${container_id} ]]; then
        if ! docker inspect "${container_id}" > "${output}/container-final.json"; then status=1; fi
        if ! docker rm --force "${container_id}" > "${output}/container-cleanup.log" 2>&1; then status=1; fi
    fi
    if ! docker image rm "${image_name}" > "${output}/image-cleanup.log" 2>&1; then status=1; fi
    exit "${status}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
jq -r --arg arch "${arch}" '.products[$arch] | to_entries[] | "\(.value)  \(.key)"' \
    "${verification}" > "${private}/products.sha256"
jq -r --arg helper "${helper_name}" '.assets[$helper]' "${verification}" > "${private}/helper.sha256"
actual=$(sha256sum < "${reviewed_bootstrap}")
printf '%s\n' "${actual%% *}" > "${private}/bootstrap.sha256"
cp "${verification}" "${output}/verification.json"
script=$(realpath "$0")
fixture="${script%/*}/fixtures/public-installer.Dockerfile"
qualification_head=$(git -C "${script%/*}" rev-parse HEAD)
script_hash=$(sha256sum < "${script}")
bootstrap_hash=${actual%% *}
jq --arg head "${qualification_head}" --arg script_hash "${script_hash%% *}" --arg bootstrap_hash "${bootstrap_hash}" \
    --arg arch "${arch}" '{tag, source, release_id, checksums_sha256, arch: $arch,
    qualification_head: $head, qualification_script_sha256: $script_hash, bootstrap_sha256: $bootstrap_hash}' \
    "${verification}" > "${output}/qualification.json"
mkdir "${output}/evidence"
docker build --platform "linux/${arch}" --tag "${image_name}" --file "${fixture}" "${private}" \
    > "${output}/image-build.log" 2>&1
docker image inspect "${image_name}" > "${output}/image.json"
image_arch=$(docker image inspect --format '{{.Architecture}}' "${image_name}")
[[ ${image_arch} == "${arch}" ]]
host_uid=$(id -u)
host_gid=$(id -g)
container_id=$(docker create --platform "linux/${arch}" --user "${host_uid}:${host_gid}" \
    --read-only --cap-drop ALL --security-opt no-new-privileges --pids-limit 256 --memory 768m \
    --tmpfs "/tmp:rw,exec,nosuid,nodev,size=256m,mode=1777" \
    --tmpfs "/home/qualification:rw,exec,nosuid,nodev,size=512m,mode=0700,uid=${host_uid},gid=${host_gid}" \
    --volume "${private}:/qualification-input:ro" --volume "${script}:/qualification.sh:ro" \
    --volume "${output}/evidence:/evidence:rw" "${image_name}" \
    bash /qualification.sh --container "${tag}" "${source}" "${arch}")
[[ ${container_id} =~ ^[0-9a-f]{64}$ ]]
docker inspect "${container_id}" > "${output}/container.json"
run_status=0
docker start --attach "${container_id}" 2>&1 | tee "${output}/container.log" || run_status=$?
container_exit=$(docker inspect --format '{{.State.ExitCode}}' "${container_id}")
[[ ${run_status} == 0 && ${container_exit} == 0 ]] ||
    fail 'clean public installer probe failed; retained evidence describes the first failure'
jq -e '.status == "pass"' "${output}/evidence/summary.json" >/dev/null
printf 'PASS native %s: public clean bootstrap, latest/pinned/reinstall, packing and conservative uninstall\n' "${arch}"
