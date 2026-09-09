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
Failed trials produce evidence and a nonzero exit status. Report, comparison, and verification commands operate offline.
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
above 5% is gated; startup gating requires a maintainer-calibrated absolute floor through
`BENCHMARK_STARTUP_FLOOR_NS`, in addition to the 15% relative threshold. Uncalibrated timing remains report-only.
Inconclusive results are visible and do not establish absence of regression. Other metrics are reported separately.

Real delegated cgroup integrations are opt-in through `MICROFAT_BENCH_CGROUP_ROOT`,
`MICROFAT_BENCH_CGROUP_VERSION`, and, for v1, `MICROFAT_BENCH_MEMORY_ROOT`. Missing environments are reported as skips.

## Release evidence

Configure repository variables `BENCHMARK_RELEASE_RUNNER_LABELS` (a JSON label array) and
`BENCHMARK_RELEASE_CONTROLS` (the target/generator settings plus `runner_identity`) before requesting release
measurement. A prerequisite job fails explicitly when this configuration is absent. Fork PRs use hosted runners;
they never execute on dedicated measurement hardware. Trusted tags/main can run the qualified release tier.

Release qualification requires complete successful trials, the configured minimum blocks, a clean source tree,
the declared runner identity, and successfully applied target/generator controls. It is a measurement eligibility
label, not independent attestation of the operator's hardware declaration. Method limitations still accompany evidence.

Successful tag runs attach a checksummed archive to the release through a separate job with publishing permissions.
Raw bundles use date, full commit, and unique experiment IDs. Uploads do not overwrite prior assets. Actions artifacts
are intermediate storage with finite retention, so a published claim must reference the release asset and its checksum.
Manual runs prepare bundles for maintainer publication. `make clean` preserves `results/`.

See [methodology](methodology.md) for scope, statistical assumptions, and interpretation limits.
