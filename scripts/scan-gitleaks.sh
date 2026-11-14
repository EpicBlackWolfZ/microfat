#!/usr/bin/env bash
# Scan the commits selected by the GitHub Actions event with the verified engine.
set -euo pipefail
IFS=$'\n\t'
gitleaks=${1:?pass the verified Gitleaks executable}
report=${2:?pass the SARIF output path}
event=${GITHUB_EVENT_NAME:?missing workflow event name}
summary=${GITHUB_STEP_SUMMARY:?missing workflow step summary path}
zero_sha=0000000000000000000000000000000000000000

event_sha() {
    jq -er "${1} | strings | select(test(\"^[a-f0-9]{40}\$\"))" "${GITHUB_EVENT_PATH:?missing workflow event payload}"
}

log_options=()
case "${event}" in
    pull_request)
        base=$(event_sha '.pull_request.base.sha')
        head=$(event_sha '.pull_request.head.sha')
        git cat-file -e "${base}^{commit}"
        git cat-file -e "${head}^{commit}"
        log_options=("--log-opts=--diff-merges=first-parent ${base}..${head}")
        ;;
    push)
        before=$(event_sha '.before')
        after=$(event_sha '.after')
        if [[ "${after}" == "${zero_sha}" ]]; then
            echo 'No commits to scan for a deleted ref.' >> "${summary}"
            exit 0
        fi
        git cat-file -e "${after}^{commit}"
        if [[ "${before}" != "${zero_sha}" ]] && git cat-file -e "${before}^{commit}" 2>/dev/null; then
            log_options=("--log-opts=--diff-merges=first-parent ${before}..${after}")
        else
            echo 'Previous push commit unavailable. Scanning the full history of the pushed ref.' >> "${summary}"
            log_options=("--log-opts=--diff-merges=first-parent ${after}")
        fi
        ;;
    workflow_dispatch) ;;
    *) printf 'Unsupported Gitleaks event: %s\n' "${event}" >&2; exit 1 ;;
esac

status=0
"${gitleaks}" git --redact=100 --exit-code=2 --report-format=sarif --report-path="${report}" \
    "${log_options[@]}" . || status=$?
case "${status}" in
    0) echo 'Gitleaks found no secrets.' ;;
    2) echo 'Gitleaks detected potential secrets. See the redacted SARIF report.' ;;
    *) printf 'Gitleaks failed with exit code %s. See the job log.\n' "${status}" ;;
esac | tee -a "${summary}"
exit "${status}"
