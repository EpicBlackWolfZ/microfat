# Runtime metadata observability recipes

These applications use the supported `github.com/EpicBlackWolfZ/microfat/runtimeinfo`
accessor. Each is a separate Go 1.27.3 module, with a local `replace` pointing
three directories up to this checkout. Their pinned observability SDKs do not
become dependencies of the core accessor or launcher. Run commands from the
recipe directory; no workspace file or published microfat version is needed.

## Prometheus

```bash
cd examples/runtimeinfo/prometheus
go run . -once
go run . -listen 127.0.0.1:9090
```

The first command prints one Prometheus scrape and exits without opening a
socket. The second serves `/metrics` until SIGINT or SIGTERM. A collector calls
`runtimeinfo.Read()` on every scrape, so observations follow runtime setting
changes. The example uses a private registry to keep the output focused.

## OpenTelemetry

```bash
cd examples/runtimeinfo/opentelemetry
go run . -timeout 5s
```

The application registers observable gauges, collects them once with a manual
reader, exports JSON through the stdout metric exporter, and exits. It requires
no collector service. In a long-running service, register the instruments and
callback once on the application's meter provider and use a periodic reader with
its chosen exporter. Do not repeatedly create providers per request.

## Meaning and trust

Both recipes publish the same subset of the snapshot:

| Prometheus name | OpenTelemetry name | Meaning |
| --- | --- | --- |
| `microfat_launcher_hint_info` | `microfat.launcher.hint.info` | Gauge 1 with selected variant and available host/dispatch hint labels or attributes. |
| `microfat_runtime_gomaxprocs` | `microfat.runtime.gomaxprocs` | Observed threads allowed to execute Go code simultaneously; not a CPU count or quota. |
| `microfat_runtime_gomemlimit_bytes` | `microfat.runtime.gomemlimit` | Observed Go soft memory limit in bytes; not RSS or a cgroup limit. |
| `microfat_runtime_memory_limit_enabled` | `microfat.runtime.memory_limit.enabled` | Whether the observed soft memory limit is enabled, 1 or 0. |
| `microfat_runtime_gogc_percent` | `microfat.runtime.gogc` | Observed GC target percentage. |
| `microfat_runtime_gogc_enabled` | `microfat.runtime.gogc.enabled` | Whether the observed GOGC percentage-based trigger is enabled, 1 or 0. |
| `microfat_launcher_cgroup_cpu_quota_hint` | `microfat.launcher.cgroup.cpu_quota.hint` | Launcher-time environment hint in CPU equivalents, including fractions. |
| `microfat_launcher_cgroup_memory_limit_hint_bytes` | `microfat.launcher.cgroup.memory_limit.hint` | Launcher-time environment hint in bytes. |

Only `StatusPresent` values produce measurements. Missing, invalid,
unsupported, and unavailable fields produce no numeric sample, rather than
zero. Launcher info is omitted without a present selected-variant hint;
unavailable auxiliary Prometheus labels are empty and OpenTelemetry attributes
are omitted. A native launch normally emits runtime observations alone.

The disabled GOMEMLIMIT sentinel (`math.MaxInt64`) omits the memory-limit byte
gauge and emits `memory_limit_enabled=0`. The disabled GOGC sentinel
(`math.MaxUint64`) omits the percentage gauge and emits `gogc_enabled=0`.
This disables the percentage-based GC trigger; a finite GOMEMLIMIT can still
trigger garbage collection. Other
present numeric values, including zero hints, are exported verbatim. A zero
memory-limit hint cannot distinguish finite zero, unavailable, or unlimited;
the accessor does not invent that missing distinction.
The exporters represent gauges as floating point numbers, so sufficiently large
integers can lose precision; use the accessor directly for exact byte counts.

Launcher metadata is untrusted environment text and may be forged or stale.
Its gauges are hints, not current cgroup probes or proof of applied tuning.
The examples deliberately do not use configured environment text or launcher
recommendations as effective runtime values. They also omit payload digests,
executable paths, and free-text policy reasons from metric labels/attributes.
Hints are not authentication, authorization, or producer identity. Keep metric
labels limited to the expected variants in your application and apply your
normal monitoring access controls.

To exercise hint handling without a packed executable:

```bash
MICROFAT_SELECTED_VARIANT=v3 MICROFAT_HOST_ARCH=amd64 \
  MICROFAT_HOST_LEVEL=v3 MICROFAT_EXEC_MODE=memfd \
  MICROFAT_CGROUP_CPUS=1.5 MICROFAT_CGROUP_LIMIT_BYTES=1073741824 \
  GOMEMLIMIT=512MiB GOMAXPROCS=2 GOGC=off go run . -once
```

Run this from the Prometheus directory. In the OpenTelemetry directory, replace
`-once` with `-timeout 5s`. These synthetic hints demonstrate parsing only;
they do not simulate a microfat launch or change actual cgroup limits.

## Verification

From each recipe directory:

```bash
go mod tidy -diff
go mod verify
go build -o /dev/null .
go vet ./...
go test -race ./...
```

Then smoke-run `go run . -once` for Prometheus and
`go run . -timeout 5s` for OpenTelemetry. The repository's root `go test ./...`
does not traverse nested modules; CI must execute these commands separately.

Direct SDK pins are from the official releases:
[Prometheus client v1.25.0](https://github.com/prometheus/client_golang/releases/tag/v1.25.0)
and [OpenTelemetry Go v1.47.0](https://github.com/open-telemetry/opentelemetry-go/releases/tag/v1.47.0).
