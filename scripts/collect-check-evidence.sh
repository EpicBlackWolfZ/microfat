#!/usr/bin/env bash
# Collect PR check runs, commit statuses, and rollup evidence.
set -euo pipefail
IFS=$'\n\t'

if [ "${#}" -lt 1 ]; then
    echo "Usage: ${0} <output-directory>" >&2
    exit 1
fi

OUTPUT_DIR="${1}"
mkdir -p -- "${OUTPUT_DIR}"

PRS=(272 273 274)

echo "Collecting check evidence into ${OUTPUT_DIR}..."

for PR_NUM in "${PRS[@]}"; do
    echo "--> Collecting PR #${PR_NUM}..."
    gh pr view "${PR_NUM}" --json number,title,state,headRefName,headRefOid,baseRefName,baseRefOid,statusCheckRollup > "${OUTPUT_DIR}/pr_${PR_NUM}.json"
    
    HEAD_SHA="$(jq -r .headRefOid "${OUTPUT_DIR}/pr_${PR_NUM}.json")"
    echo "    Head SHA: ${HEAD_SHA}"
    
    gh api "repos/EpicBlackWolfZ/microfat/commits/${HEAD_SHA}/check-runs?per_page=100" --paginate > "${OUTPUT_DIR}/check_runs_${PR_NUM}.json"
    gh api "repos/EpicBlackWolfZ/microfat/commits/${HEAD_SHA}/status" > "${OUTPUT_DIR}/status_${PR_NUM}.json"
    
    if ! gh pr checks "${PR_NUM}" > "${OUTPUT_DIR}/pr_checks_${PR_NUM}.txt" 2>&1; then
        echo "    Notice: non-zero exit from gh pr checks ${PR_NUM}"
    fi
done

echo "Check evidence collection complete in ${OUTPUT_DIR}."
