# Runtime metadata for applications

Import `github.com/EpicBlackWolfZ/microfat/runtimeinfo` and call `Read()` to collect
informational launcher metadata and current Go runtime settings. The package uses
only the Go standard library. Importing it and reading a snapshot do not tune the
runtime, change the environment, open cache files or discover cgroups.

```go
snapshot := runtimeinfo.Read()
if snapshot.Launcher.SelectedVariant.Status == runtimeinfo.StatusPresent {
    fmt.Println("launcher variant hint:", snapshot.Launcher.SelectedVariant.Value)
}
if snapshot.Observed.GOMAXPROCS.Status == runtimeinfo.StatusPresent {
    fmt.Println("observed GOMAXPROCS:", snapshot.Observed.GOMAXPROCS.Value)
}
```

The three groups have different sources:

| Group | Meaning |
| --- | --- |
| `Launcher` | Environment hints reported by a launcher, including its cgroup observations and calculated recommendations. These can be forged, inherited or stale. |
| `Configured` | Current environment text, which may have been set by a user, launcher or application. It does not identify who set it or prove that the runtime applied it. |
| `Observed` | Current settings read from this Go process through `runtime/metrics`, including programmatic tuning after startup. |

Both full and minimal launchers export the same metadata contract. Native programs
normally have missing launcher hints and available runtime observations.
`runtimeinit.AutoTune` applies settings and returns its own result; it does not
export launcher metadata. No field is a certificate that microfat launched this
process. In particular, a payload digest or original-executable hint is not an
authentication, authorization or producer-identity proof. `OriginalExecutable`
preserves the hint verbatim; use `runtimeinit.Executable` for the existing
asset-resolution fallback behavior.

## Availability and parsing

Each `Field[T]` has `Value`, `Raw`, `Status` and `Source`. Use `Value` only when
`Status == StatusPresent`. `Source` is the exact environment key or
`runtime/metrics:` followed by the metric name. Environment `Raw` preserves the
original text even when parsing fails; runtime observations have no raw text.

| Status | Meaning |
| --- | --- |
| `present` | Available; parsed successfully for typed numeric/enum/digest fields, or copied verbatim for opaque strings. |
| `missing` | The environment key is absent. No default or effective value is inferred. |
| `invalid` | A numeric value is malformed, outside the accepted range or overflows; or a digest is malformed. |
| `unsupported` | A known enum field contains a value outside this accessor's supported contract. Raw text remains available. |
| `unavailable` | A requested runtime metric is unavailable or has an unexpected metric kind. |

Opaque strings, including configuration and level/policy names, are not validated:
a present empty string remains present. Typed numeric fields reject empty text,
signs, whitespace, exponent notation and non-ASCII digits. Integer parsing uses
decimal signed 64-bit bounds. Quota parsing accepts nonnegative finite decimal
numbers with an optional fractional part. No field failure fails the snapshot.
Unknown future environment keys are ignored, and known keys are interpreted
independently without trying to repair contradictory hints or infer their age.
Repeated calls refresh values, but a call is not an atomic process snapshot.

## Launcher fields

All sources in this table have the prefix `MICROFAT_`.

| `Launcher` field | Source suffix | Interpretation |
| --- | --- | --- |
| `SelectedVariant` | `SELECTED_VARIANT` | Opaque selected architecture level, such as `v3` or `v8.0`; no compatibility inference. |
| `HostArch` | `HOST_ARCH` | Supported values `amd64`, `arm64`; unknown values are unsupported. |
| `HostLevel` | `HOST_LEVEL` | Opaque launcher-detected level. |
| `ExecMode`, `DispatchMode` | `EXEC_MODE`, `DISPATCH_MODE` | Reported actual mode: `memfd` or `cache`. The latter is the legacy alias; both are read independently. Native configuration such as `auto` is unsupported as an actual-mode hint. |
| `SelectedSHA256` | `SELECTED_SHA256` | Exactly 64 hexadecimal characters, normalized to lowercase in `Value`. This reads a claim; it does not verify bytes. |
| `SelectedSizeBytes` | `SELECTED_SIZE` | Positive uncompressed payload bytes. |
| `OriginalExecutable` | `ORIGINAL_EXE` | Opaque informational asset-resolution path; no filesystem validation. |
| `PolicyApplied`, `OverrideReason` | `POLICY_APPLIED`, `OVERRIDE_REASON` | Opaque selection policy/reason text; an empty reason can be present. |

`Launcher.Cgroup` uses these sources, also prefixed `MICROFAT_`:

| Field | Source suffix | Interpretation |
| --- | --- | --- |
| `Version` | `CGROUP_VERSION` | Integer `1` or `2`; other nonnegative integers are unsupported. |
| `MemoryLimitBytes` | `CGROUP_LIMIT_BYTES` | Nonnegative hard-limit observation. `0` alone cannot distinguish absent/unlimited from finite zero in the launcher contract. |
| `MemoryHighBytes` | `CGROUP_HIGH_BYTES` | Nonnegative throttle boundary, only exported when finite. A present zero is finite zero. |
| `EffectiveMemoryLimitBytes` | `CGROUP_EFFECTIVE_LIMIT_BYTES` | Nonnegative effective hard/throttle ceiling, only exported when finite. A present zero is finite zero; absence is unknown to this reader. |
| `CPUQuota` | `CGROUP_CPUS` | CPU equivalents, rounded by the launcher to two decimal places. `0.00` does not prove unlimited quota: it can also reflect absent data or a tiny rounded quota. |
| `RecommendedGOMEMLIMIT` | `CGROUP_GOMEMLIMIT` | Opaque Go memory-limit configuration text, currently an integer with a `B` suffix. This field does not parse it into a byte count. |
| `RecommendedGOMAXPROCS` | `CGROUP_GOMAXPROCS` | Positive recommended execution-thread count. |
| `RecommendedGOGC` | `CGROUP_GOGC` | Nonnegative heap-growth percentage, or exact `off` represented as `-1`. |
| `GCProfile` | `CGROUP_GC_PROFILE` | Supported `default`, `latency_critical`, `memory_constrained`, `batch_etl`, `adaptive`. The launcher normally omits its default profile; absence stays unknown. |
| `GOGCSkippedReason` | `CGROUP_GOGC_SKIPPED_REASON` | Opaque explanation for an omitted GC recommendation. |

These observations date from launcher dispatch, not the current cgroup state.
Recommendations can be present with `MICROFAT_AUTOTUNE=0`, dry run or explicit
`GOMEMLIMIT`, `GOMAXPROCS` and `GOGC` overrides. They do not establish that tuning
was applied. Byte counts are not physical quotas, and a Go memory budget is not
an RSS cap. Missing profile metadata does not imply a default was applied.

## Configuration and current observations

`Configured.GOMEMLIMIT`, `.GOMAXPROCS` and `.GOGC` copy those exact environment
keys. The remaining fields copy `MICROFAT_CPU_POLICY`, `MICROFAT_GC_PROFILE`,
`MICROFAT_AUTOTUNE`, `MICROFAT_DRY_RUN`, `MICROFAT_MEM_RATIO` and
`MICROFAT_LIVE_HEAP_ESTIMATE` into `CPUPolicy`, `GCProfile`, `Autotune`, `DryRun`,
`MemoryRatio` and `LiveHeapEstimate`, respectively. All are opaque strings, with
no parsing, validation or default resolution. A supplied live-heap estimate is not
a measured heap. This reader does not recompute recommendations; doctor owns
diagnostic tuning previews.

| `Observed` field | Runtime metric | Units and sentinel |
| --- | --- | --- |
| `GOMEMLIMITBytes` | `/gc/gomemlimit:bytes` | Unsigned bytes; `math.MaxInt64` means no Go memory limit. This soft budget does not cap RSS. |
| `GOMAXPROCS` | `/sched/gomaxprocs:threads` | Unsigned count of threads that can execute Go code simultaneously; not a physical CPU count. |
| `GOGCPercent` | `/gc/gogc:percent` | Unsigned heap-growth percentage; `math.MaxUint64` means the percentage trigger is off. A finite memory limit can still trigger garbage collection. |

No time/duration, live cgroup limit, application identity, effective CPU policy,
applied GC profile or measured heap is synthesized from the exported hints.

## Observability recipes

[Prometheus and OpenTelemetry examples](../examples/runtimeinfo/README.md) live in
separate Go modules, so their SDKs do not enter the core accessor's dependencies.
They include commands that collect once and exit, and label launcher hints and
observed runtime settings explicitly. They handle disabled-GC and unlimited-budget
sentinels without emitting misleading huge percentage/budget gauges. `GOGC=off`
does not disable memory-limit-driven garbage collection. Avoid using
arbitrary environment paths or digests as metric labels.

Run `task test-runtimeinfo-examples` to compile and exercise both recipes.
The regular race/coverage pipeline covers the accessor and native full/minimal
memfd/cache integration. CI also checks both example modules and their dependencies.
