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

A standalone Task recipe builds both program families, tiers v1/v2/v3, a baseline launcher and the hashed manifest without depending on another POC branch:

```bash
task -t experiments/research161/corpus.Taskfile.yml corpus \
  GO=/absolute/Go1.27.3/bin/go OUTPUT=.work/research161-corpus
```

Run this on Linux amd64; it deliberately omits v4 execution and ARM64 qualification. It uses the exact strip/build flags below and records the current repository commit in the generated manifest. `OUTPUT` must be new. The helper manifest subcommand can also record already-built files, and refuses to overwrite an existing manifest. The retained campaign used the earlier shared corpus and launcher hashes recorded in its evidence; a rebuilt launcher may change absolute artifact sizes while the report still charges its full actual size equally to every candidate.

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

## Retained first screen, 2026-10-23

The [demo report](evidence/demo.json), [microfat report](evidence/microfat.json), [corpus manifest](evidence/corpus-manifest.json) and [runner provenance](evidence/provenance.json) retain all 26 candidate results and input identities. The native Linux amd64 corpus contains two stripped program families at tiers v1/v2/v3 from source `ada376b4ffffaa3354becbfb70c1896934caf277`, built with Go 1.27.3, `-trimpath -buildvcs=false -ldflags='-s -w -buildid='`. The runner used `GOMAXPROCS=2`, `GOFLAGS=-p=2` and Zstd `better`. Team measurements were serialized; host controls were inherited, without an imposed CPU affinity, quota, RSS ceiling or pressure workload.

| Family | No dictionary | Current, 32 KiB requested | Current, 112 KiB requested | Smallest new selector |
|---|---:|---:|---:|---:|
| demo | 9,626,985 B | 9,623,774 B | **9,606,459 B** | section/32 KiB: 9,643,659 B |
| microfat | 18,188,638 B | 18,195,957 B | **18,184,166 B** | section/32 KiB: 18,214,724 B |

The 112 KiB request produces **96 KiB of actual history** because 393,216 training bytes are capped at one quarter. The complete trained dictionaries contain 98,445 B (demo) and 98,450 B (microfat), including their headers. All artifacts passed exact round trips and the production full-artifact verifier. The report ID `none` means **Zstd without a dictionary**, not the uncompressed codec.

The current trainer saves only 20,526 B (0.2132%) and 4,472 B (0.0246%) of the complete artifacts at the larger budget. A successful dictionary is not automatically beneficial: the 32 KiB current dictionary makes the microfat artifact 7,319 B larger than no dictionary. Every new selector loses to both the best current candidate and no dictionary on these two families. This is evidence to stop added selection complexity on this candidate pool, while retaining a no-dictionary comparison in any future automatic-selection study. It is not a general result about all ELF corpora or a production change.

Uniform sampling finds 96 distinct windows out of 96 for demo and 95 out of 96 for microfat, with **zero exact all-variant core windows** in either family. Thus the fixed-window core/accessory and prevalence hypotheses have almost no signal here. Section sampling finds 5 and 4 core windows respectively, but its complete artifacts are still larger. This does not refute shared short substrings, instruction-aware matches, other programs or an independently measured compression-affinity table; those are different candidate spaces and require a separate justified budget.

The one-shot train/assembly diagnostics are approximately 129 ms without a dictionary versus 151 ms for the smallest demo artifact, and 308 ms versus 350 ms for microfat. These include in-memory round-trip verification and are not production pack times. Their cumulative Go allocation counts are approximately 196/222 MB and 325/355 MB respectively; **these are allocation volume, not live or peak memory**. Raw per-variant decoder diagnostics are preserved, but no decode-speed, startup or p99 improvement is inferred from one run in a fixed candidate order. No complete launcher execution, held-out family, unstripped corpus or controlled cgroup test was performed.

The next useful extension is a small confirmatory corpus with entirely different program families and stripped/unstripped builds, first testing whether simply choosing the cheaper of the current dictionary and no dictionary is worthwhile. A complex biological selector, superstring implementation or format change has no support from this screen.
