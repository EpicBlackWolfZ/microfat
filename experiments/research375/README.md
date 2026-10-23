# Codec concurrency proof of concept (#375)

This isolated screening tool tests the resource-headroom hypothesis without changing
production formats, dispatch, cache handling, decoder bounds or concurrency defaults.
It runs real streaming Zstd encode/decode, verifies the exact output size and SHA256,
and optionally runs an explicitly trusted, native Go ELF to known readiness output.
It is an experiment harness, not a new microfat launcher.

The [native screening result](RESULTS.md) preserves the first run and its evidence.

```bash
GOMAXPROCS=4 task research-375 INPUT=/absolute/path/demo-v1 FAMILY=demo \
  SOURCE=$(git rev-parse HEAD) TRIALS=3 > .work/research375-demo.json
```

Build the CLI once before collecting timing. Running `go run` invokes the compiler
before the report, but not inside measured samples. A direct invocation supports
multiple canonical-order inputs for the encoder bundle and explicit child readiness:

```bash
go build -o .work/research375 ./experiments/research375
GOMAXPROCS=4 .work/research375 \
  --source-revision="$(git rev-parse HEAD)" --family=demo \
  --input=/absolute/path/demo-v1 --input=/absolute/path/demo-v2 \
  --encoders=-1,1,2,4 --workers=1,2,4 --decoders=-1,1,2,4 --bursts=1,2,4 \
  --cpu-budget=4 --trials=3 --execute \
  --payload-arg=--startup-only --payload-arg=--json \
  --ready-output=$'{\n  "status": "ready",\n  "version": "1.0.0"\n}\n' \
  > .work/research375-demo-ready.json
```

Inspect the fixture's actual stdout before supplying `--ready-output`. Only execute
trusted corpus programs with bounded, terminating readiness arguments. Input paths
must be nonempty regular native Linux amd64/arm64 Go ELF files; dictionaries and
worker request/frame files also receive descriptor-based regular-file checks.
FIFO inputs fail without waiting for a writer. CPU variant compatibility remains the
operator's responsibility. Foreign or emulated timing is not native evidence.

`-1` preserves the upstream codec default: encoder GOMAXPROCS, decoder
min(GOMAXPROCS,4). Explicit 1 disables async streaming. Each child reports its actual
GOMAXPROCS and effective requested concurrency. Allocations exceeding
outer_workers * effective_encoder_threads <= cpu-budget are explicitly listed as
omitted. The inequality limits requested work, not actual workspace RSS or CPU use.
There is no silently substituted concurrency control.

The optional `--dictionary` uses a trained Zstd dictionary, hashed and snapshotted
alongside the corpus. Repeat without a dictionary for its independent ablation.
All decoder comparisons use a fixed frame encoded with concurrency 1, so differences
in encoder bitstreams cannot confound decoder settings. Encoder samples retain
compressed bytes and SHA256 to reveal size or determinism changes.

The JSON report retains every attempted sample and failures. A failed setup or
campaign emits a partial JSON report and exits unsuccessfully. Trials rotate codec
settings; this reduces one order effect but does not remove thermal drift or make
adjacent samples independent. Keep separate reports for held-out program families,
levels, dictionary policies, externally imposed resource controls and arrival gaps.

| Field | Meaning |
| --- | --- |
| `source_revision`, `harness_sha256`, `inputs` | Caller-pinned source, actual harness binary hash and corpus hashes/bytes |
| `duration_ns`, `measurement_kind` | Codec encode, streaming decode+SHA256, or decode+SHA256+materialize+native child readiness |
| `decode_hash_ns`, `application_ns` | Codec setup/decode/hash versus temp-file write + native child execution/readiness |
| `release_ns`, `completion_ns` | Parent timestamps relative to batch start; their difference is post-release completion latency |
| `process_wall_ns` | Entire worker lifetime, including process startup, the synchronized gate and peer/arrival-gap waiting |
| `process_cpu_ns`, `worker_max_rss_kib` | Worker rusage; encoder includes independent decode verification, child readiness has additional process work |
| `before`, `after` | Actual inherited cgroup limits, raw host PSI and accessible cgroup-v2 counters; unavailable controls remain explicit |

Decoder workers parse their configuration and signal ready before the parent releases
the synchronized burst. `--arrival-gap=1ms` spaces parent releases; actual release
timestamps expose scheduling jitter. Input frame reads occur after release and before
the worker's codec timer. The parent completion interval includes them. Decoder
`duration_ns` excludes frame reads, process startup and gate waiting. Encoder codec
time excludes source reads/hash, process startup and roundtrip verification; encoder
batch wall/CPU/RSS include them. Outer workers are separate processes with fresh
codec instances, unlike production packing's single process: this screening can
reject poor allocations but cannot establish a production pack-throughput gain.

Per-worker RSS highwaters cannot be summed to claim simultaneous aggregate peak
memory. `memory.peak`, if accessible, is the containing cgroup's cumulative highwater,
including other processes and preceding work. PSI snapshots are endpoint cumulative
observations, not time-resolved attribution or a controlled pressure experiment.
No CPU quota, affinity, background pressure or memory ceiling is imposed here. Use a
fresh dedicated external cgroup for each controlled resource cohort, record its
configuration, and verify the resulting inherited limits in the report. GOMAXPROCS
is not a CPU quota. Zstd max-memory/window limits do not cap process RSS.

The hard caps are four worker processes, four explicitly requested codec threads,
eight inputs, 32MiB per ELF, 64MiB total input, 33MiB per compressed frame, 1MiB
dictionary, 4KiB application stdout/stderr, 20 trials, 30 seconds per batch and ten
minutes for the campaign. The parent kills only its spawned process group on batch
timeout. Codec calls are byte-bounded; parent baseline frame compression finishes
before the next context check. No unconditional startup delay or adaptive policy
is added.

Three trials are a smoke test. Launches in a burst are correlated. Preserve the raw
data and analyze independent burst-level maxima/SLO failures with #377 before any
tail claim; a small-sample maximum is not an established p99. Predeclare a practical
gain and memory ceiling, use held-out confirmations, and stop if a simple fixed cap
is equivalent. This buffered/temp-file pipeline says nothing directly about full
microfat memfd/cache launches, descriptor verification, seals or warm verified-cache
latency. Those require the unchanged production runtime matrix and #376 lifecycle
measurements.

```bash
GOMAXPROCS=2 GOFLAGS=-p=2 go test -race -cover ./experiments/research375
golangci-lint run ./experiments/research375/...
```
