# Reproducible server benchmarks

The canonical benchmark pipeline builds identical ISA payloads for native execution and microfat packaging,
runs a counterbalanced schedule, and retains raw Fortio output with resource observations and derived reports.
The existing `microfat benchmark` baseline and v1 evidence remain supported.

## Run an experiment

Use Go 1.27.1 and install the pinned external tool explicitly:

```bash
make benchmark-tools
make benchmark BENCHMARK_CONFIG=benchmarks/config/smoke.json
```

The setup command checks the Fortio module checksum and source commit in `benchmarks/tools.lock.json`.
Experiments do not install tools. Override `MICROFAT_BENCH_FORTIO` to use another local build of the pinned version;
the experiment records the executable hash and build information.

For full CLI control:

```bash
make build
bin/microfat benchmark run --config benchmarks/config/smoke.json \
  --fortio .work/benchmark-tools/fortio --output-dir results/benchmarks
bin/microfat benchmark verify <bundle-directory>
bin/microfat benchmark report --input <bundle-directory> --format markdown
bin/microfat benchmark compare --input <bundle-directory> --json
```

The run command prints the resulting bundle path on stdout; progress and diagnostics go to stderr.
Failed trials produce evidence and a nonzero exit status. Interrupted journals are included in CI artifacts.
Raw trial JSON is deterministic and compact; the harness bounds retained trial data to 512 MiB and complete
bundles to 768 MiB, allowing full cgroup telemetry for a 200-trial release shard. Per-file limits still apply.
Report, comparison, and verification commands operate offline.
`report` and `verify` also accept original v1 evidence files. For a raw v1 payload without its envelope, verification
explicitly reports structural validation only; it cannot establish an absent checksum.

## Configure the suite

JSON suite files override validated defaults. Unknown fields and out-of-range settings are errors.

| Setting | Meaning |
| --- | --- |
| `workload` | `mixed`, `cpu`, or `memory`; deterministic handlers with bounded work |
| `blocks`, `seed` | Matched trial blocks and reproducible counterbalanced schedule |
| `duration_ms`, `warmup_ms` | Measured window and separately retained warmup |
| `payload_bytes`, `iterations`, `concurrency` | Request work and maximum in-flight requests |
| `qps` | Requested total QPS; zero selects maximum-throughput mode |
| `resolution_seconds` | Fortio histogram resolution; recorded with latency results |
| `sample_ms` | Separate observer process sampling interval |
| `disable_observer` | Disable telemetry only for observer overhead calibration; prevents release eligibility |
| `format`, `profile`, `codec`, `dictionary` | Executable format 1/2, full/minimal, zstd/lz4/none, optional zstd dictionary |
| `specialized_level` | Host-compatible ISA level; defaults to the detected level |
| `cache_states` | `cold` and/or `warm` microfat materialization state |
| `tuning` | `matched` explicit runtime settings, or a separate `on-off` memfd experiment |
| `gomaxprocs`, `gomemlimit`, `gogc` | Identical explicit runtime settings for matched comparisons |
| `exec_diagnostics` | Additional traced execution pass, excluded from primary timing measurements |
| `runner_identity` | Maintainer-declared identity of a qualified dedicated measurement host |
| `target`, `generator` | Separate affinity masks and delegated cgroup settings |

Example resource settings, requiring actual delegated cgroups and CPU masks allowed by the host:

```json
{
  "target": {
    "affinity": [2, 3],
    "cgroup_root": "/sys/fs/cgroup/delegated-benchmarks",
    "version": "v2",
    "cpu_quota_us": 200000,
    "cpu_period_us": 100000,
    "memory_bytes": 2147483648
  },
  "generator": {
    "affinity": [4, 5],
    "cgroup_root": "/sys/fs/cgroup/delegated-benchmarks",
    "version": "v2",
    "cpu_quota_us": 200000,
    "cpu_period_us": 100000,
    "memory_bytes": 2147483648
  }
}
```

For v1, provide `version: "v1"`, the CPU controller in `cgroup_root`, and the memory controller in `memory_root`.
The tool creates child groups, verifies settings, and attaches each child before executing the target or generator.
It does not enable parent controllers, change host frequency policy, or flush global page caches. Unavailable
controls produce explicit uncontrolled results. Resource-control failures are distinct from workload failures.

CPU masks must be disjoint; inspect recorded sibling topology when selecting physically separate cores.
Process separation alone cannot eliminate shared cache, memory-bandwidth, or kernel interference.

## Validation and revision comparisons

```bash
make benchmark-integration
make benchmark-matrix
python3 scripts/benchmark-matrix.py compatibility --list
python3 scripts/benchmark-matrix.py nightly --workload cpu
```

The compatibility matrix enumerates 16 format/profile/codec/dictionary combinations. Each case includes native,
memfd, cold materialization, and warm materialization execution. Unsupported host ISAs fail selection conservatively;
cross-compilation does not count as measured native execution.

To compare two revisions, keep this checkout as the target source/harness and provide a separate base checkout:

```bash
BENCHMARK_BASE=/absolute/path/to/base-checkout make benchmark-smoke
```

The same payload bytes are packaged by base and current packers/stubs. Both revisions are interleaved within each
trial block. Native controls use those same payloads. This measures packaging/launcher revision changes; it does
not silently change the workload source when comparing a base that predates this benchmark suite.

PR CI reports coarse startup/size regressions and retains raw evidence without posting comments. Size growth
above 5% is gated. Startup enforcement requires both a 15% increase and an independently calibrated absolute
floor. CI reads `benchmarks/config/calibration.json` from the trusted baseline checkout. Unknown runner classes,
image updates, changed measurement settings, and insufficient or unstable calibration remain explicitly report-only.
The local `--startup-calibrated` / `--startup-floor-ns` flags remain available for an explicitly supplied policy;
workflow execution uses `benchmark gate --calibration <trusted-policy.json>` instead of an unversioned variable.

## Real kernel controls

`make benchmark-kernel` requires `MICROFAT_BENCH_CGROUP_ROOT`, `MICROFAT_BENCH_CGROUP_VERSION`, and for v1,
`MICROFAT_BENCH_MEMORY_ROOT`. It fails if required controls are unavailable. Ordinary unit tests still skip
privileged integration when no root is configured; normal benchmark runs retain explicit uncontrolled fallbacks.

Hosted v2 jobs run natively on amd64 and arm64. `scripts/benchmark-controls.sh <controls.json>` creates a private
empty delegated subtree plus a manager leaf. `scripts/benchmark-enter.sh "$MICROFAT_BENCH_CGROUP_ROOT" <command>`
moves only the new command into that leaf and immediately drops back to the runner user. This keeps child
migrations inside a writable common ancestor without changing the workflow runner's own cgroup or its ownership.
Cleanup removes only the empty benchmark-created groups.

`make benchmark-kernel-v1` boots a networkless QEMU TCG guest. The kernel and matching modules package are locked
in `benchmarks/kernel.lock.json`, checked against Ubuntu-signed metadata, and checked again after download.
The guest records kernel configuration, binary/initramfs hashes, controls, counters, and serial test results.
Missing completion, test skips, or timeout fail validation. Emulation proves kernel behavior and is never used
as performance evidence. No KVM or nested virtualization support is required.

## Hosted release evidence

All configured benchmark jobs use standard public GitHub-hosted runners. No paid runner, cloud account, homelab,
or self-hosted registration is required. CPU masks select separate available vCPUs; physical cores, hypervisor
neighbors, host frequency, and filesystem page-cache state remain outside the experiment's control.

Nightly and release runs compare base/head inside the same VM and shard by amd64/arm64, mixed/CPU/memory,
and standard/heavy intensity. Nightly compares the trusted parent. Release runs resolve the previous stable
release ancestor, excluding the current revision; absence of such an ancestor fails rather than substituting.
Release trials use 20 blocks, 10-second warmup and 30-second measurement. The compatibility tier exercises
16 format/profile/codec/dictionary cases with native, memfd, cold-cache and warm-cache configurations.

`microfat benchmark qualify --policy hosted-release --input <bundle>` verifies publication completeness:
clean matching harness/source, 20 paired blocks with at least 10-second warmup and 30-second measurement,
sampling at most every 250 ms, effective target/generator controls, core measurements,
and valid telemetry. It prints a JSON verdict and exits nonzero on failure. `release_eligible` retains its strict
controlled-hardware meaning and is always false on hosted runners. A measured regression can still be published
as evidence; the publication verdict does not claim improved performance. Partial exec diagnostics and host noise
remain visible qualifications. Dedicated hardware certification is future work in issue #182.

Dispatch `benchmarks.yml` with `tier=calibration` to collect 20 training and 10 holdout jobs, grouped by actual
runner class. The candidate absolute floor is twice the largest absolute no-change training median difference;
all holdout jobs must pass the coarse gate. The workflow verifies every bundle and produces a candidate policy
for review, without automatically committing it. A class lacking enough independent jobs stays report-only.
A policy records job identities and checksum-manifest digests so its inputs can be audited. The initial
[30-job calibration](https://github.com/EpicBlackWolfZ/microfat/actions/runs/34450243438) split across nine CPU/image
classes; none met both per-class sample counts, so the committed policy keeps startup enforcement report-only.
All input bundles were verified and replayed offline after correcting the CLI newline framing in the aggregator.

Dispatch `tier=release` on a repository branch for a nonpublishing rehearsal; `tier=compatibility` runs the
compatibility checks alone. Completed rehearsal bundles are downloaded into a fresh verification job, checked
again, replayed byte-for-byte, and audited for all 12 sustained shards before an archive is prepared. Only a
trusted tag run attaches the archive and checksum to the release, without replacing previous assets. Failed
or interrupted evidence remains diagnostic and cannot pass matrix publication. Artifacts are scoped to the run
attempt; rerun all jobs when retrying a complete matrix so evidence from different attempts cannot be mixed.
Maintainers control tags/releases.

Smoke and integration artifacts expire after seven days; sustained, calibration and rehearsal artifacts after
fourteen days. Archives omit temporary build files and guest images. Published claims must reference the durable
release archive, checksum, source/baseline, runner details and limitations. This documentation makes no speedup claim.

See [methodology](methodology.md) for scope, statistical assumptions, and interpretation limits.
