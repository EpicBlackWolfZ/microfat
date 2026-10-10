# Cache-first auto startup measurements

[**← Architecture**](architecture.md)

Cache-first auto used an existing verified extraction cache on every warm launch in this AMD64 experiment.
With format v2, the full stub and zstd, mean full-process startup changed from **11.743 ms to 5.340 ms**.
With an absent extraction cache, auto continued through memfd and measured **11.802 ms before and 12.058 ms after**.
These are measurements from one host and workload, with OS page caches warmed.

## Method and scope

The measurements were taken on 2026-10-10 with Task 3.53.1 and Go 1.27.2 on an AMD Ryzen 9 3950X
(32 logical CPUs), running Linux `7.2.9-200.nobara.fc44.x86_64`. The host selected AMD64 v3 from
the demo's v1/v2/v3/v4 variants.

The same expanded startup runner measured the baseline binaries built from committed source
`c0caa941750896dc45ad2e6a18efd4f7b69b1342` and candidate binaries containing cache-first auto.
The baseline matrix ran first. The candidate matrix ran after the cache socket rejection,
materialization preflight, telemetry and diagnostic changes, following local native qualification.
The SHA-256 hashes identify both measured stub sets:

| Stub | Baseline SHA-256 | Candidate SHA-256 |
| --- | --- | --- |
| Full | `622d874e2ea5f202fa6151ec60778ff11aa54c65f65c5e4cef8c6d8d2e7c2831` | `4455f714b7acfbe8c3f0e978fe599b3f31ebaf693ff2872a118af90339a6822c` |
| Minimal | `0e7a5b8f80a872e2b4a650af5635107ccc93a1534d0ea353e6311b14790e7187` | `7eb915945d786c80d9e7ef330146684693ec1f9b284ecd6ac4dd78ccde3cce82` |

These hashes identify the exact local binaries used by this experiment.

Each observation launches a fresh process with `--startup-only`, checks successful exit and `READY`
output, and measures wall time from process execution through child exit. This includes launcher,
kernel and application initialization cost. Dispatch telemetry records the selected variant, actual
execution mode, launcher time and decompression time separately.

Every child used identical tuning:

```text
GOMAXPROCS=1
GOMEMLIMIT=off
GOGC=100
MICROFAT_AUTOTUNE=0
MICROFAT_LOG=json
```

The runner removes inherited launcher controls and conflicting Go tuning before applying these values.
Each scenario receives five warmup launches and 50 measured launches. A child has a ten-second timeout;
failed execution, missing readiness, incomplete telemetry or an unexpected explicit mode fails the matrix.

The complete matrix contains 80 combinations, plus four controls:

| Dimension | Values |
| --- | --- |
| Format | v1, v2 |
| Stub profile | full, minimal |
| Codec | zstd, lz4, none, zstd-dict |
| Execution/cache state | memfd/absent, cache/absent, cache/warm, auto/absent, auto/warm |
| Additional controls | native v1, native v3, trimmed memfd, optimized native |

Each run therefore contains **84 scenarios and 4,200 measured launches**. All 4,050 fat launches per
run selected v3. All explicit modes dispatched as requested. Baseline auto used memfd for both cache
states. Candidate auto used memfd for all 800 absent-cache observations and cache for all 800 warm-cache
observations. Every candidate warm auto observation reported zero payload decompression time.

An absent-cache scenario gets a new private directory and pins `MICROFAT_CACHE_DIR` to an absent leaf
inside it for every launch. A warm scenario pins the same variable to a directory populated by an
explicit cache launch before measurement. Both states also set `XDG_CACHE_HOME`. Explicit cache paths
prevent automatic discovery from finding entries in a global fallback directory.

Here, **cold means an absent extraction cache**. Executable and OS page caches are warmed; the runner
does not drop page caches. CPU affinity and frequency were not controlled. The matrix establishes
successful behavior on this host and shows observed process latency; it is not a statistical regression
gate or qualification of another CPU, filesystem, kernel or ARM64 environment.

## Representative results

The table covers every execution/cache state for each codec with format v2 and the full stub.
All values are full-process wall time in milliseconds. The other format/profile combinations are
included in the raw matrix observations.

| Codec | Requested mode/cache | Before mean | After mean | Before p50 | After p50 | Before p95 | After p95 |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| zstd | memfd/absent | 11.738 | 11.765 | 11.722 | 11.692 | 12.160 | 12.296 |
| zstd | cache/absent | 18.534 | 18.909 | 18.548 | 18.906 | 18.957 | 19.592 |
| zstd | cache/warm | 5.289 | 5.542 | 5.239 | 5.400 | 5.737 | 6.273 |
| zstd | auto/absent | 11.802 | 12.058 | 11.716 | 11.901 | 12.141 | 13.022 |
| zstd | auto/warm | 11.743 | 5.340 | 11.721 | 5.265 | 12.155 | 5.853 |
| lz4 | memfd/absent | 9.173 | 9.186 | 9.194 | 9.169 | 9.375 | 9.524 |
| lz4 | cache/absent | 16.034 | 16.796 | 15.995 | 16.573 | 16.346 | 18.262 |
| lz4 | cache/warm | 5.241 | 5.737 | 5.210 | 5.683 | 5.749 | 6.626 |
| lz4 | auto/absent | 9.159 | 9.357 | 9.142 | 9.264 | 9.380 | 10.123 |
| lz4 | auto/warm | 9.191 | 5.377 | 9.194 | 5.315 | 9.455 | 5.833 |
| none | memfd/absent | 6.770 | 7.041 | 6.784 | 6.973 | 6.998 | 7.580 |
| none | cache/absent | 13.540 | 13.552 | 13.526 | 13.538 | 13.916 | 13.860 |
| none | cache/warm | 5.306 | 5.305 | 5.287 | 5.266 | 5.644 | 5.666 |
| none | auto/absent | 6.685 | 6.767 | 6.684 | 6.783 | 6.952 | 7.047 |
| none | auto/warm | 6.775 | 5.271 | 6.811 | 5.245 | 7.064 | 5.746 |
| zstd-dict | memfd/absent | 17.514 | 17.647 | 17.496 | 17.570 | 17.888 | 18.098 |
| zstd-dict | cache/absent | 24.329 | 24.374 | 24.258 | 24.349 | 25.112 | 24.759 |
| zstd-dict | cache/warm | 5.283 | 5.282 | 5.186 | 5.229 | 5.770 | 5.664 |
| zstd-dict | auto/absent | 17.466 | 17.476 | 17.448 | 17.461 | 17.829 | 17.802 |
| zstd-dict | auto/warm | 17.516 | 5.205 | 17.514 | 5.187 | 17.890 | 5.574 |

Warm cache execution still opens, validates and hashes the selected ELF on every launch.
The measured warm latency is nonzero. Same-UID processes can modify a writable cache inode after a
check; these measurements do not establish cache immutability. See the [cache trust policy](architecture.md)
and [security boundaries](../SECURITY.md).

## Reproduce

From the repository root, with the required pinned tools installed:

```bash
mkdir -p .work/cache-first-startup
task bench-startup STARTUP_ITERATIONS=50 \
  STARTUP_CSV="${PWD}/.work/cache-first-startup/startup.csv"
```

The root Task command rebuilds the CLI and both stub profiles before running the matrix.
The CSV preserves every measured observation, including requested mode, extraction-cache state,
actual dispatch mode, selected variant, wall time and telemetry. Keep raw runs locally and compare
equivalent tuning and cache states. The local issue #44 evidence is stored under `.work/issue-44`;
raw CSV and logs are excluded from version control.
