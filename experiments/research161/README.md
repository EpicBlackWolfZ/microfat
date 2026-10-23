# Dictionary sampling screen (#161)

This isolated POC writes complete Format v2 fat artifacts and compares their actual byte counts. It does not change production packing, formats, launchers or defaults. The candidates are no dictionary, the current trainer, balanced per-variant history, exact-window deduplication, frequency/prevalence ordering, alternating core/accessory history, and ELF-section sampling. No superstring algorithm or specialist dictionary pool is implemented.

All history alternatives except section sampling keep the **same training contents** and change only dictionary history. This separates the prefix-history question from training-statistics changes. Frequency sorts exact windows by distinct-variant prevalence and then occurrence count. Core means present byte-for-byte in every variant; core/accessory alternates one unique core and one unique accessory window, then fills from the remaining group. When every window is unique, prevalence provides little information; the report records that negative diagnostic. Exact hash buckets are followed by byte comparison.

Uniform sampling reproduces the current 32 evenly spaced 4 KiB windows per variant. Section sampling cycles through file-backed ELF `PROGBITS` sections at least 4 KiB long, selecting 32 4 KiB windows per file. At most eight variants and 64 MiB per input are allowed, so the sample budget stays below the production 4 MiB ceiling. Each requested history budget is capped at one quarter of actual sample bytes, as in the production trainer. Zstd dictionary headers are additional bytes; the reported complete artifact includes them, every frame, the launcher, the exact serialized index and the trailer.

Run with Go 1.27.3 and pinned Task:

```bash
GOMAXPROCS=2 GOFLAGS=-p=2 task research-161 \
  MANIFEST=/absolute/path/corpus/manifest.json FAMILY=demo \
  STUB=/absolute/path/microfat-stub OUTPUT=.work/research161-demo \
  SIZES=32768,114688 LEVEL=better
```

`OUTPUT` must be a new directory with an existing parent. It receives a `report.json` and one executable `.fat` per successful candidate. Inputs are read once, hashes and lengths are checked against the manifest, ELF class/endianness/architecture are checked, and immutable in-memory snapshots feed every candidate. The experiment does not execute input or output binaries. Use a launcher built for the same target; SHA-256 and full stub size are recorded. The manifest format is:

```json
{
  "schema_version": 1,
  "source_revision": "full source commit",
  "go_version": "go1.27.3",
  "variants": [
    {"family": "demo", "level": "v1", "path": "/absolute/demo-v1", "sha256": "full hash", "bytes": 123456},
    {"family": "demo", "level": "v2", "path": "/absolute/demo-v2", "sha256": "full hash", "bytes": 123456}
  ]
}
```

Retain the manifest, exact source/build recipe (including strip flags, architecture tiers and toolchain), and output together. Reports retain every attempted candidate and error. Successful candidates require byte-for-byte decompression **and** the expected SHA-256, followed by the production complete-artifact verifier. The smallest candidate is only a size winner for this bundle; no dictionary remains eligible. The index creation timestamp is fixed at zero to make artifacts deterministic across reruns at the same dependency/toolchain/settings. Timing fields naturally vary.

The one-shot train/assembly wall duration includes compression and round-trip checks but excludes input I/O, complete-artifact verification and file publication. Allocation counters are process-wide Go allocations during that interval, **not peak RSS**. Per-variant compression and decompression durations are codec diagnostics in one process. They do not establish cold process latency, independent samples, application readiness, p99, CPU usage, cgroup behavior or production pack memory. Run timing experiments serially and retain resource controls. Warm verified-cache launches skip decompression, so they remain a negative control for any subsequent launch study.

This is a first screen on supplied families, with no fitted heuristic tuning. Before promotion, predeclare a meaningful complete-byte threshold and pack/startup/memory ceilings, pin separate stripped/unstripped families and toolchains, and confirm on held-out whole program families. Stop selection complexity if the current trainer or no dictionary is equally good. Follow-up startup work must use independently sampled launches, preserve all failures, and keep explicit memfd, auto cache miss, warm verified cache and cold cache materialization separate. Descriptor hashes, size bounds and sealing remain mandatory.
