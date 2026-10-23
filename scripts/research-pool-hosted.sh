#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

# This bounded adapter is only for disposable, native GitHub-hosted VMs.
if [[ "${GITHUB_ACTIONS:-}" != true ]]; then
  echo 'The hosted adapter requires an ephemeral GitHub Actions VM.' >&2
  exit 1
fi
research_arch="${RESEARCH_ARCH:?native architecture required}"
research_replica="${RESEARCH_REPLICA:?VM replica required}"
research_go="${GO:-go}"
research_host_arch="$("${research_go}" env GOHOSTARCH)"
if [[ "${research_arch}" != "${research_host_arch}" ]]; then
  echo 'Native architecture mismatch; emulation cannot qualify performance.' >&2
  exit 1
fi
research_cpus="$(getconf _NPROCESSORS_ONLN)"
if [[ "${research_cpus}" -lt 4 ]]; then
  echo 'This protocol requires four native CPUs.' >&2
  exit 1
fi
research_root="${PWD}/.work/research-pool-hosted"
if [[ -e "${research_root}" || -L "${research_root}" ]]; then
  echo 'Hosted output directory must be new.' >&2
  exit 1
fi
mkdir -p "${research_root}/evidence"
research_evidence="${research_root}/evidence"
research_revision="$(git rev-parse HEAD)"
research_host="${research_arch}-vm-${research_replica}"

preserve_research_evidence() {
  research_status=$?
  trap - EXIT
  # Preserve structured reports and diagnostic logs, never executable payloads.
  if [[ -d "${research_root}/runs" ]]; then
    (
      cd "${research_root}"
      find runs -type f \( -name '*.json' -o -name '*.log' -o -name '*.txt' \) \
        -exec cp --parents -- '{}' "${research_evidence}" \;
    ) || research_status=1
  fi
  printf '%s\n' "${research_status}" > "${research_evidence}/adapter-exit-status.txt"
  exit "${research_status}"
}
trap preserve_research_evidence EXIT
{
  printf 'source_revision=%s\nhost_id=%s\n' "${research_revision}" "${research_host}"
  date -u '+started_utc=%Y-%m-%dT%H:%M:%SZ'
  uname -a
  lscpu
  "${research_go}" version
  git status --short
} > "${research_evidence}/host.txt"

# Start an ordinary user manager with delegated controls. All later trial units
# are fresh, bounded and owned by the harness; no external workloads are killed.
research_uid="$(id -u)"
research_user="$(id -un)"
sudo loginctl enable-linger "${research_user}"
sudo systemctl start "user@${research_uid}.service"
sudo systemctl set-property --runtime "user@${research_uid}.service" Delegate=yes
export XDG_RUNTIME_DIR="/run/user/${research_uid}"
export DBUS_SESSION_BUS_ADDRESS="unix:path=${XDG_RUNTIME_DIR}/bus"
systemctl --user show-environment >/dev/null

task research-375-pool-prepare GO="${research_go}" POC_ROOT="${research_root}/training" \
  > "${research_evidence}/training-build.log" 2>&1
task research-heldout-corpus GO="${research_go}" CORPUS_OUTPUT="${research_root}/heldout" CORPUS_ARCH="${research_arch}" \
  > "${research_evidence}/heldout-build.log" 2>&1
cp -- "${research_root}/training/manifest.json" "${research_evidence}/training-manifest.json"
cp -- "${research_root}/heldout/manifest.json" "${research_evidence}/heldout-manifest.json"
cp -- "${research_root}/heldout/sources.lock.json" "${research_evidence}/sources.lock.json"
cp -- "${research_root}/heldout/SHA256SUMS" "${research_evidence}/corpus-SHA256SUMS"
cp -- "${research_root}/heldout/"*.module.json "${research_evidence}/"
cp -- "${research_root}/heldout/"*.buildinfo "${research_evidence}/"
mkdir "${research_root}/runs"
research_binary="${PWD}/.work/research375pool/encoder-pool"
sha256sum -- "${research_binary}" > "${research_evidence}/producer-SHA256SUMS"

# The calibration rule and confirmation sample size are committed before this
# adapter runs. Held-out observations cannot alter the frozen training policy.
"${research_binary}" --mode=calibrate --manifest "${research_root}/training/manifest.json" \
  --stub "${research_root}/training/stub-full" --source-revision "${research_revision}" \
  --host-id "${research_host}" --blocks=3 --output "${research_root}/runs/training" \
  > "${research_evidence}/calibrate.stdout.json" 2> "${research_evidence}/calibrate.stderr.log"
"${research_binary}" --mode=freeze --request "${research_root}/runs/training/report.json" \
  --output "${research_root}/policy.json" \
  > "${research_evidence}/freeze.stdout.json" 2> "${research_evidence}/freeze.stderr.log"
cp -- "${research_root}/policy.json" "${research_evidence}/policy.json"
sha256sum -- "${research_root}/policy.json" > "${research_evidence}/policy-SHA256SUMS"
"${research_binary}" --mode=confirm --manifest "${research_root}/heldout/manifest.json" \
  --stub "${research_root}/heldout/stub-full" --source-revision "${research_revision}" \
  --host-id "${research_host}" --blocks=30 --policy "${research_root}/policy.json" \
  --output "${research_root}/runs/confirmation" \
  > "${research_evidence}/confirm.stdout.json" 2> "${research_evidence}/confirm.stderr.log"
"${research_binary}" --mode=confirm-summary --request "${research_root}/runs/confirmation/report.json" \
  > "${research_evidence}/confirmation-summary.json" 2> "${research_evidence}/summary.stderr.log"

# One held-out family covers the entire format/profile/codec/dispatch matrix;
# all four families separately have confirmation integrity/equality checks.
jq '.variants |= map(select(.family == "actionlint" and .profile == "stripped")) |
    .sources |= map(select(.family == "actionlint"))' "${research_root}/heldout/manifest.json" \
  > "${research_evidence}/matrix-manifest.json"
"${research_binary}" --mode=matrix --manifest "${research_evidence}/matrix-manifest.json" \
  --stub "${research_root}/heldout/stub-full" --minimal-stub "${research_root}/heldout/stub-minimal" \
  --source-revision "${research_revision}" --trials=1 --execute=true --output "${research_root}/runs/matrix" \
  > "${research_evidence}/matrix.stdout.json" 2> "${research_evidence}/matrix.stderr.log"
