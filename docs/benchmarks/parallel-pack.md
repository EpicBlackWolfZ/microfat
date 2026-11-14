# Native parallel Pack qualification for v0.4.0

This protocol qualifies the production Pack implementation from #415. It
adapts #389’s hosted VM matrix, fixed paired blocks, separate fallback
accounting and exact median bounds. It uses the repository’s owned cgroup v2
delegation instead of the prototype’s user systemd units. Commit the protocol,
schedule and collector before gathering timings.

## Frozen matrix

Dispatch **Benchmarks → parallel-pack** on the reviewed commit. Run on native
Linux amd64 and arm64, with two independent GitHub-hosted VMs per family and
architecture. Each VM runs one pinned family (Fortio, Task, actionlint or
govulncheck), both binary profiles (stripped and unstripped), and all three Pack
profiles (latency, balanced and size). The pinned corpus recipe supplies three
ISA tiers, the full stub and ELF digests. Build with the Go patch in the
checked-out root go.mod (1.27.4 at protocol inception). The controller checks
the corpus toolchain, architecture, revision, file sizes and hashes before
timing.

Use CPU quotas of one and four cores and memory.max of 128, 256 and 512 MiB or
max, with memory swap disabled. Each arm gets a fresh cgroup leaf and the same
CPU quota, GOMAXPROCS, GC environment, corpus, profile and epoch. The serial
arm caps outer variant workers at one. The default arm uses production
admission. Each successful worker records the selected count, so a default
run that falls back to one remains visible. Each pair runs consecutively.
Twenty-four blocks rotate the order of the 48 cells per VM and alternate arm
order. No machine’s blocks are pooled with another’s. Time only the complete
Pack call, including input preparation and atomic output finalization. Process
startup, cgroup setup and hashing are outside the timer. The input page cache
is warm or unspecified.

This plans 16 VM shards, 768 per-VM cell comparisons and 2,304 attempts per
shard. The 300-minute shard timeout, two-minute worker timeout, and 2 GiB
minimum free output space are fixed. A timing timeout remains a failed attempt.
A control or disk setup failure stops that shard and retains the remaining
schedule as missing. Never rerun a slow or failed observation to substitute a
better result. A rerun of the entire workflow is a separate campaign.

## Decision rules

Each pair needs successful worker exits, the observed one-worker baseline and
valid default worker count, cgroup controller readbacks, no OOM event and equal
complete artifact sizes and SHA-256 hashes. Both arms’ cgroup memory.peak,
memory.events counters, cpu.max and memory.max are retained before teardown.
Any whole-cgroup peak above the finite cap, changed complete artifact, newly
introduced OOM event or default Pack failure against a successful serial Pack
is a regression. A failed serial baseline without a candidate-only OOM, or
any missing or uncontrolled pair, leaves the cell inconclusive in the absence
of a recorded regression. Missing and failed pairs stay in the denominator.

The speed estimand is the median within-block default/serial Pack wall-time
ratio. For a complete set of 24 pairs, report the third and twenty-second
ordered ratios as the two-sided bounds. Under independent, stationary block
ratios the exact binomial tail probability is 602/16,777,216 per comparison.
Across all 768 comparisons the union bound is below the 5% family error
budget. Scheduling does not establish independence. Report the median of
available pairs descriptively when a bound cannot be calculated.

A lower bound above 1.05 is a regression. An upper bound at or below 1.05 is a
non-regression pass when all safety and artifact checks pass and the candidate
consistently parallelizes or consistently falls back in that cell. An upper
bound strictly below 0.90 demonstrates more than 10% lower packing time for a
consistently parallel cell. Mixed admission remains inconclusive, with each
worker count and measured time retained. A consistently fallback cell’s pass
qualifies fallback only. A shard reports regression if any cell regresses,
then inconclusive if any cell is inconclusive, and otherwise pass. A campaign
needs every shard for an overall pass.

Review summary.json, plan.json, per-attempt input/intent, logs and
observations, corpus metadata, and source and producer identities. Complete
artifact hashes, sizes and failure observations are recorded before deleting
the attempt's staging files. Upload all outcome records without executable files. Publish all
shard results, including failed or incomplete shards. Document any acceptance
failure in the release notes or revisit admission on a newly reviewed protocol
with the original results retained.
