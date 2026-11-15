#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

report=${ADVISORY_REPORT:-.work/advisory-scan}
summary=${GITHUB_STEP_SUMMARY:-/dev/null}
repository=EpicBlackWolfZ/microfat
status=0

# Freeze one current upstream snapshot so all scans use the same advisory data.
fetch_database() {
    local metadata digest
    curl --fail --silent --show-error --retry 3 --max-time 60 https://vuln.go.dev/vulndb.zip -o "${report}/vulndb.zip"
    sha256sum "${report}/vulndb.zip" > "${report}/vulndb.sha256"
    unzip -q "${report}/vulndb.zip" -d "${report}/vulndb"
    metadata=$(jq -er .modified "${report}/vulndb/index/db.json")
    read -r digest _ < "${report}/vulndb.sha256"
    printf '### Go vulnerability database\n\nUpstream modified: %s  \nSnapshot SHA-256: %s\n' "${metadata}" "${digest}" >> "${summary}"
}

# Keep running after findings so a single advisory never hides other results.
# Text mode preserves govulncheck's exit status and distinguishes analysis errors.
scan() {
    local name=$1 mode=$2 toolchain=$3
    shift 3
    local result=0 log="${report}/${name}.log"
    govulncheck -db="file://${report}/vulndb" -format=text -show=version,verbose,traces -mode="${mode}" "$@" > "${log}" 2>&1 || result=$?

    {
        printf '\n#### %s (%s)\n\n' "${name}" "${toolchain}"
        case "${result}" in
            0) printf 'Scan completed without a failing finding.\n' ;;
            3) printf 'Govulncheck reported findings (exit 3).\n' ;;
            *) printf 'Scan error (exit %s).\n' "${result}" ;;
        esac
        printf '\n'
        if (( result == 0 || result == 3 )); then
            # Include the result level so package/module observations remain distinct from symbol findings.
            sed -n -E '/^(=== .* Results ===|Vulnerability #[0-9]+: GO-|[[:space:]]+Fixed in:)/s/^/    /p' "${log}"
        else
            tail -n 20 "${log}" | sed 's/^/    /'
        fi
    } >> "${summary}"

    sed 's/^/  /' "${log}"
    if (( result != 0 )); then
        printf '::error::Govulncheck %s (%s mode) exited %s. See the job summary and scan log.\n' "${name}" "${mode}" "${result}"
        status=1
    fi
}

scan_source() {
    local toolchain module source
    toolchain=$(go env GOVERSION)
    source=$(git rev-parse HEAD)
    {
        printf '### Source scans\n\nCommit: %s  \nGo toolchain: %s  \n' "${source}" "${toolchain}"
        printf 'Source-mode symbol findings include call traces in the logs. Package and module observations are listed separately.\n'
    } >> "${summary}"
    scan root source "${toolchain}" ./...
    for module in prometheus opentelemetry; do
        scan "${module}" source "${toolchain}" -C "examples/runtimeinfo/${module}" ./...
    done
}

prepare_release() {
    local tag version source
    gh api "repos/${repository}/releases/latest" --jq \
        '{tagName: .tag_name, isDraft: .draft, isImmutable: .immutable, isPrerelease: .prerelease, publishedAt: .published_at}' \
        > "${report}/release.json" || return
    tag=$(jq -er 'select(.isPrerelease == false).tagName | select(test("^v[0-9]+\\.[0-9]+\\.[0-9]+$"))' "${report}/release.json") || return
    printf 'Release: %s  \n' "${tag}" >> "${summary}" || return
    gh api "repos/${repository}/commits/${tag}" --jq .sha > "${report}/source.txt" || return
    go run ./internal/cmd/release-audit metadata "${tag}" "${report}/release.json" "${report}/source.txt" || return
    version=${tag#v}
    source=$(<"${report}/source.txt") || return
    printf 'Release source: %s  \n' "${source}" >> "${summary}" || return
    gh release download "${tag}" --repo "${repository}" --dir "${report}/dist" \
        --pattern "microfat_${version}_linux_*" --pattern "microfat-install_${version}_linux_*" --pattern 'checksums.txt*' || return
    env -u GH_TOKEN go run ./internal/cmd/release-audit prepare-advisory --dist "${report}/dist" --output "${report}/prepared" \
        --version "${version}" --source "${source}"
}

scan_release() {
    local arch binary name toolchain
    printf '\n### Published release binary scans\n\n' >> "${summary}"
    # prepare_release checks each command explicitly so we can retain its error.
    # shellcheck disable=SC2310
    if ! prepare_release > "${report}/verification.log" 2>&1; then
        {
            printf '\nPublished release acquisition or signed-asset verification failed.\n\n'
            tail -n 30 "${report}/verification.log" | sed 's/^/    /'
            if [[ -s "${report}/prepared/commands.jsonl" ]]; then
                jq -r '(.stderr // ""), (.error // "")' "${report}/prepared/commands.jsonl" | tail -n 20 | sed 's/^/    /'
            fi
        } >> "${summary}"
        printf '::error::Published release acquisition or signed-asset verification failed.\n'
        sed 's/^/  /' "${report}/verification.log"
        return 1
    fi
    unset GH_TOKEN
    printf 'Signed assets verified. Binary-mode findings describe symbol observations without source call traces.\n' >> "${summary}"
    printf 'Without usable symbol information, govulncheck can report at module-level precision.\n' >> "${summary}"
    for arch in amd64 arm64; do
        for binary in "${report}/prepared/${arch}/"*; do
            name="${arch}-${binary##*/}"
            toolchain=$(go version -m -json "${binary}" | jq -er .GoVersion)
            scan "${name}" binary "${toolchain}" "${binary}"
        done
    done
    if [[ ${ADVISORY_FAILURE_PROBE:-false} == true ]]; then
        printf 'Controlled non-Go scan input\n' > "${report}/failure-probe.txt"
        scan failure-probe binary 'controlled scan error' "${report}/failure-probe.txt"
    fi
}

mkdir -p "${report}"
report=$(realpath -e "${report}")
case ${1:-} in
    database) fetch_database ;;
    source) scan_source ;;
    release) scan_release ;;
    *) printf 'Usage: advisory-scan.sh database|source|release\n' >&2; exit 2 ;;
esac
exit "${status}"
