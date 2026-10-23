# Encoder-pool training and held-out confirmation protocol

This successor to the #386 screening retains its opt-in implementation and
serial production defaults. Commit this protocol and code before building the
revision-pinned research executable or inspecting any new timings. Calibration
uses only demo/microfat; the four external application families cannot train the
policy. The protocol identifier and its SHA-256 are recorded in every study and
frozen policy, alongside producer revision/binary, manifest and training-report
hashes. Source control identifies the complete method, not just that identifier.

## Native populations and corpus

The planned confirmation family contains **16 cells**: four held-out families
on native amd64/arm64, with two fresh GitHub-hosted VMs per architecture. Each VM
calibrates and freezes its own policy before confirming. Blocks from different
VMs are not pooled as independent machines. An unavailable architecture or VM
leaves the broad production decision incomplete; no emulated timing substitutes.

The shared `research-heldout-corpus` Task recipe pins Fortio 1.76.0, Task 3.54.0,
actionlint 1.7.12 and govulncheck 1.9.0. Schema2 retains source commits, module and
go.mod checksums, toolchain/build flags, architecture and every ELF hash/size.
There are three ISA tiers and stripped/unstripped profiles per family; neither
tiers nor profiles count as additional independent applications. Schema2 bounds
are 32 variants, 96 MiB per file and 1 GiB total. Legacy schema1 bounds remain
eight variants, 32 MiB per file and 64 MiB total. The larger bound accommodates
the actual Task binaries; no family is dropped or shrunk to fit an old bound.

Native Linux, Go1.27.3, at least four usable CPUs, a working systemd user manager,
delegated CPU/memory/swap controllers and readable cgroup telemetry are required.
Control setup/readback failures stop and preserve an incomplete report. Inputs
are hashed before trials: page cache is warm or unspecified, never claimed cold.

## Training and the frozen policy

`calibrate` runs exactly three rotating paired blocks for demo/microfat,
CPU4, hard caps 128/256/512 MiB and declared workspace reserves 32/64/96 MiB:
108 attempted fresh worker units, current versus pool. Inner concurrency is one
for the pool. No low-cap failure is hidden or rerun into success.

`freeze` selects the **largest** tested reservation whose CPU4/512 MiB training
cells both have all three successful, complete, compatible pairs; all pool jobs
admit three workers; each recorded whole-worker cgroup peak is at most 384 MiB;
and each family's median paired current/pool ratio is strictly above 10/9.
All lower-cap observations remain diagnostic. If no reserve qualifies, the
frozen policy disables pooling and records the negative result.

The policy is conservative and deterministic before held-out timings:

- Use current `Pack` at CPU1 or hard caps below 512 MiB.
- At CPU4/high memory, require exactly three variants whose raw-byte sum plus
  three declared workspace reservations and a conservative 16 MiB shared-stub
  reserve fit within 75% of the hard cap. Otherwise use current `Pack`.
- Eligible inputs use three outer workers and explicit inner concurrency one.
  The stub must fit the shared reserve. No dictionary is used in confirmation.

The reservation is an admission rule, **not an RSS or cgroup-usage guarantee**.
Preparation, ELF/ABI validation, retained heap, codec behavior and file cache can
consume memory outside its accounting. Actual cgroup charges and worker max RSS
are retained separately. Do not tune the reserve using held-out outcomes.

## Fixed confirmation and decision gates

Every VM runs **30 fixed rotating paired blocks** per held-out family at
CPU4/512 MiB, stripped, full stub, Zstd/default, format2: 240 attempts. Three
additional paired diagnostic blocks per family at CPU1/512 MiB and
CPU4/128/256 MiB add 72 attempts, for 312 total. The primary estimand is the
population median of within-block current/policy complete-Pack wall-time ratios.
Pack includes preparation, compression, assembly and atomic finalization;
process startup, controller/verification and systemd orchestration are separate.
Policy selection uses pinned manifest sizes in the controller, outside the
reported Pack call. A future user-facing selector's own overhead is not qualified
by these Pack-call measurements.

For each complete 30-pair primary cell, the lower median bound is the **eighth
ordered ratio**. Its binomial lower-tail probability is at most 1/(20×16).
Integer binomial counts select the rank; rational arithmetic sorts the ratios
and compares thresholds, preserving exact boundary cases. A union bound spends
at most family alpha1/20 across the 16 predeclared cells. This coverage assumes
independent stationary block ratios; a fresh process or read-back cgroup limit
does not establish that assumption. There is no normal approximation, bootstrap,
optional stopping, sample-size update or p99 inference.

Per-VM qualification requires:

- At least three of four source-size-eligible families. Every eligible cell's
  one-sided lower ratio bound must be **strictly greater than 10/9**, equivalent
  to more than 10% packing-time reduction. A 1.10 ratio is insufficient.
- A noneligible primary family remains current/current. It must complete all
  30 pairs and its same allocated lower bound must exceed **20/21**, excluding
  more than 5% packing-time regression. It receives a safe-fallback label, never
  a pooling-win claim. These eligibility choices use input metadata, not timings.
- Any failed/missing primary attempt, incorrect requested/actual controls,
  decoded-payload mismatch, compressed compatibility difference, invalid logical
  counters or native correctness failure vetoes that VM. Failed/missing pairs
  do not disappear into successful-pair intervals.
- In low-resource diagnostics, current success/policy failure or a changed
  fallback path vetoes qualification. A failed baseline instead marks that
  resource class unsupported, retaining both exit/OOM outcomes without a speed
  or equivalence claim. This distinction is fixed before timing: an infeasible
  baseline is not attributed as a pooling regression or improvement.

All four native VM cohorts must qualify before a broad production proposal.
Even then, default changes require review and the normal compatibility gates.
Unstripped sensitivity is explicitly descriptive: no additional formal interval
or promotion claim is allocated to those observations.

Every artifact is decoded/hash-verified against source bytes. Complete size,
compressed-frame hashes, dictionary identity and canonical metadata are checked.
Compressed-frame identity is an additional strict compatibility gate: a valid
alternative Zstd frame is a compatibility difference, not decoded corruption.
No failed gate will be weakened after looking at confirmation results.

## Correctness, retention and replay

After timed confirmation completes, the retained first primary artifact in each
arm/family receives untimed direct-native, explicit memfd/cache and prewarmed
auto checks. Version commands are allowlisted and bounded; exact stdout and
actual dispatch mode/variant/hash/size must match. These terminating commands
measure completion correctness, not network-service readiness or launch p99.
The summary independently requires all four native records for every first
primary artifact in both arms; an incomplete native phase vetoes qualification.
The child environment fixes `GOVERSION=go1.27.3`. Govulncheck's base allowlisted
metadata remains `-version`, but its effective correctness command additionally
uses `-db file://<owned-directory>`: an immutable test-only local fixture with
`index/modules.json` equal to `[]\n` and `index/db.json` equal to
`{"modified":"2026-01-01T00:00:00Z"}\n`. Effective arguments and both files'
absolute paths, lengths and SHA-256 hashes are recorded. All four modes share
the same path, and the files are checked before every launch. Missing or changed
files veto correctness even if govulncheck would ignore metadata errors. Setup
and validation occur outside Pack timers; the owned fixture survives until
launch records are captured. It is never used for a vulnerability scan. This
removes external DB refreshes from exact version-output comparisons without
changing the pinned shared source recipe or lock file.
One small held-out family (actionlint, stripped) separately covers both format
versions, full/minimal stubs and none/LZ4/Zstd/dictionary, current versus pool,
at a 1 GiB hard cap. It is not a second performance-selection experiment.

Disk retention is bounded by cohort count rather than trial count. Keep block0
artifacts in each family/profile/resource/workspace/arm and all failed outputs.
For later successful trials, first fsync a complete `observation.json` and its
directory, then remove only that trial's verified owned `artifact` path. The
durable record says **delete intent**; the final report records **completed
removal**. Errors never authorize deleting a foreign path or hiding an attempt.
Raw logs, requests, sizes, frame hashes, indexes, counters and source identities
remain for every attempt. Hosted uploads retain those records and corpus build
metadata, without executable payloads. Reconstruct ELF artifacts from pinned
recipes if an independent decode audit needs the pruned files.

Before each study trial, a read-only filesystem check requires at least **2 GiB
available**. Each check records available bytes and the threshold outside Pack
timing. Insufficient space or unavailable telemetry stops before launching that
trial, preserves completed attempts and records an incomplete report/stop reason.
This is a preflight guard, not a filesystem quota or atomic disk reservation.
The native qualification collects stripped timing only; unstripped packing
timing is not part of this campaign.

Each native VM runs the committed adapter:

```sh
task research-375-pool-hosted GO=/absolute/path/to/go1.27.3
```

The adapter requires disposable GitHub-hosted VMs and `RESEARCH_ARCH` plus
`RESEARCH_REPLICA`; it does not modify user-manager configuration on a desktop.
For manual quiet-slot collection, use the same executable and fixed plan:

```sh
.work/research375pool/encoder-pool --mode=calibrate --blocks=3 \
  --host-id=amd64-vm-1 --manifest TRAIN/manifest.json --stub TRAIN/stub-full \
  --output NEW_TRAIN
.work/research375pool/encoder-pool --mode=freeze --request NEW_TRAIN/report.json \
  --output NEW_POLICY.json
.work/research375pool/encoder-pool --mode=confirm --blocks=30 \
  --host-id=amd64-vm-1 --manifest HELDOUT/manifest.json --stub HELDOUT/stub-full \
  --policy NEW_POLICY.json --output NEW_CONFIRM
.work/research375pool/encoder-pool --mode=confirm-summary --request NEW_CONFIRM/report.json
```

The revision-pinned build provides the source-revision default. Retain binary,
policy, protocol and raw-report hashes; later documentation commits do not
relabel measured source. Do not tune, silently retry, discard unfavorable cells,
or pool these ratios with codec-pipeline or application-launch measurements.
