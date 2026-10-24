# Compression profiles: isolated application-telemetry diagnostic (C2)

This is a separate prospective diagnostic for #42, following the original C1
[complete-cost protocol](codec-policy.md), frozen at
`df579d1`. Commit this document and the isolated collector implementation before
any C2 timing. Keep C1's complete schedule, raw failures, source identities and
failed disposition. Do not pool C1/C2, replace C1 observations or treat C2 as a
successful rerun of C1. No production behavior or default policy changes.

## Identified cause and the changed question

The pinned govulncheck source calls `telemetry.Start(ReportCrashes: true)` before
parsing `-version`. Its linked `golang.org/x/telemetry` revision
`f71dc92da45e` starts a crash-reporting sidecar unless its stored mode is off,
using `os.Executable()` and `exec.Command`. Under memfd, that path is
`/memfd:microfat_payload`, which cannot be reopened as a normal filesystem path.
C1 preserves the emitted re-execution error and fails exact output validation.
Native/cache executions can start a real sidecar instead, so filtering stderr
would conceal different payload work. The sidecar also starts a new session;
the collector's ordinary owned-process-group cancellation cannot contain that
separate background workload. There is no codec-specific fix in this study.

The source locations are govulncheck `cmd/govulncheck/main.go`, and the pinned
telemetry module's `start.go` and `internal/telemetry/dir.go`. The latter uses
`os.UserConfigDir` and reads `go/telemetry/mode`. This linked library does not
read `GOTELEMETRY=off` or `GOTELEMETRYDIR` as opt-out environment variables.
These claims come from the exact downloaded source, not a current-library API
assumption. Retain those module versions/checksums and explanatory source
excerpts/locations with the evidence.

C2 asks for the complete codec costs when this auxiliary application telemetry
is disabled equally for every arm. It does not assess the telemetry-enabled
compatibility of govulncheck or other self-reexecuting applications. It does not
estimate the cost of telemetry by comparing C1 and C2; their collection times,
host conditions and configurations differ.

## Fixed isolation and provenance

Select this study explicitly with `CODEC_STUDY=isolated`; original remains the
collector's default. Use a new output directory and a clean source commit.
Before the wrapper's first collector compilation/Go environment command, create a fresh,
private, adjacent bootstrap configuration containing `go/telemetry/mode` with
exact contents `off 1970-01-01\n`. Refuse an existing bootstrap configuration.
Preserve it, its absolute path and identity as preparation evidence.
Task resolves shared dynamic toolchain variables using short ambient `go env`
commands before invoking the wrapper, even when the target does not reference
them. Those reads are outside collection. This protocol isolates all wrapper,
compiler and collector subprocesses; it does not claim every parent Task
metadata read or unrelated host process uses the private configuration.

Inside the new evidence directory, create `config/go/telemetry/mode` with those
same exact bytes. Directories have mode 0700 and the mode file 0600. Use its
absolute `config` directory as `XDG_CONFIG_HOME` for every preparation command,
Pack worker, initial native reference, timed native control and fat launch.
Remove inherited `XDG_CONFIG_HOME` before appending that one controlled value.
Keep the bootstrap and evidence configurations distinct and explicitly recorded.
Do not change any user or system telemetry configuration.

Freeze the selected study/protocol, effective configuration paths, exact mode
contents and SHA-256 identities along with source, collector build settings,
compiler/linker, source lock, stubs, CLI and payload identities. Recheck the
configuration before timing and after collection; any change fails the final
authoritative qualification status. C2 preparation must not silently fall back
to the ambient user configuration. The source-built corpus remains byte-matched
to rebuilt current-source stubs/CLI before collection.

## Unchanged fixed work and decisions

The corpus, eight alternatives, raw-empty `CompressionLevel`, epoch zero,
Format v2, source-built full/minimal stubs, baseline-tier execution,
GOMAXPROCS 4, autotuning off, per-attempt private caches, local govulncheck DB,
budgets, ordering, validation and statistics are exactly as in C1:

- 264 fresh full-stub Pack workers: eleven groups, eight settings, three rounds.
- 88 matched, untimed minimal preparations, with complete verification.
- 10560 fat launches: eleven groups, eight settings, two stubs, three paths,
  twenty rounds; plus 1320 unchanged native controls.
- 120-second/2-GiB observed-RSS Pack budget; ten-second/512-MiB launch budget.
  These remain observed process high-water budgets, not imposed cgroup caps.
- Complete artifact/component bytes, wall/CPU/wait4 RSS, exact output,
  descriptors/cache identities, all failures and all intended denominators.
- The same paired-round medians, absolute savings and 2000-resample, seed-42,
  cell-wise 95% bootstrap intervals against the three existing profile anchors.

The original practical margins and conservative profile-replacement criteria
remain unchanged. There are no pilots, extra samples, selective settings,
replacement attempts, trimming or adaptive extensions within C2. A preparation
failure is incomplete and retained. Finish the planned cohort after individual
attempt failures where possible, and retain them in the denominator.

Report every comparison, failed setting and inconclusive control. C2 supports
only the isolated configuration on the named shared native amd64 workstation;
it does not establish a fleet or service-p99 result, disk-cold behavior, native
arm64 timing, telemetry-enabled compatibility or cgroup peak memory. As in C1,
the pre-launch full-file identity scan warms source pages outside timing.
The three Pack rounds are descriptive, and repeated tiers/profiles remain
observations of four families rather than independent applications.

Retain current defaults unless the completed evidence justifies a separate
reviewed production design. An apparent C2 winner is a candidate for independent
held-out confirmation, not authority to add a predictor, automatic dictionary
search, threshold or new decoder. A green C2 result cannot override C1's failure
or establish general memfd self-reexecution support. A failed/inconclusive C2
must remain explicit; no further timing cohort is scheduled by this protocol.

## Reproduction and retention

Build a new clean-source corpus with the pinned Task recipe, outside timing.
Then run the explicit study once:

```bash
task codec-policy-corpus CODEC_CORPUS="$PWD/.work/codec-policy-corpus-c2" CODEC_ARCH=amd64
task test-codec-policy-harness
task qualify-codec-policy CODEC_STUDY=isolated \
  CODEC_CORPUS="$PWD/.work/codec-policy-corpus-c2" \
  CODEC_OUTPUT="$PWD/.work/codec-policy-cohort-c2"
```

Archive C1 and C2 separately under `docs/benchmarks/codec-42/`, including original
protocols, complete raw schedules/observations, preparation outcomes, final
source/configuration checks and failed/passing status. Retain the source recipe,
payload/artifact identities and verification when excluding large local binaries.
Record a single disposition describing both cohorts and their separate limits.
Pinned Task source validation, coverage, compatibility and exact-head native
hosted CI remain independent requirements; this study does not authorize merge
or release and cannot resolve #230's historical per-trial service-p99 endpoint.
