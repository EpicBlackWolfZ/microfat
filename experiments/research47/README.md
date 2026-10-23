# Exact-sharing opportunity screen (#47)

This is an offline, bounded size diagnostic. It does not change the production
format, packer, launcher or defaults. It reconstructs exact ELF bytes without
executing them. It is the entry gate for #379, not a graph architecture or a
production delta decoder.

## Frozen protocol, before held-out results

Use the shared checksum-pinned corpus recipe in `experiments/heldoutcorpus`:
Fortio, Task, actionlint and govulncheck, each stripped and unstripped, each at
three native ISA tiers. These four program families were absent from the earlier
demo/microfat sampling and pooling screens. Stripping profiles and tiers are
paired observations, not additional independent families. All groups remain in
the results, including unsupported inputs, failed candidates and size losses.

Each family/profile gets actual complete Format v2 artifacts from unchanged
`pack.Pack`, with Zstd/default and either no dictionary or the current 112 KiB
requested dictionary. The smaller complete artifact is the size control. Its
real stub, dictionary, frames, metadata and 56-byte trailer all count. Artifact
verification must match the pinned original payload sizes and SHA-256 hashes.
No sampler from #161 is retuned. There is no fitted heuristic across families.

The prespecified partitions are whole ELF sections plus every gap/header byte,
fixed 512-byte blocks, fixed 4096-byte blocks and bounded Gear content-defined
chunks (minimum 1024, average-mask 4096, maximum 16384 bytes). The CDC table uses
SplitMix64 seed 47. SHA-256 buckets are followed by exact byte comparisons.
Sharing means present in at least two distinct tiers; universal and subset-shared
coverage are reported separately. Gross duplicate coverage is not compressed
size savings. Repeated chunks within only one tier are not shared storage.

For each partition, one actual research container stores a compressed shared
pool, one compressed literal residual per tier, and a compact ordered path per
tier. Adjacent contiguous references coalesce. Paths use a kind byte and unsigned
varint offset/length; each path is compressed only when that is smaller. Pool,
residual and path hashes plus original output sizes/hashes are in the strict JSON
header, whose SHA-256 occupies a 32-byte footer. Actual framing/header/path bytes
all count. Every target must reconstruct with identical bytes and SHA-256.
The pool is one Zstd frame: selecting one target requires decoding the **whole
pool**, which is charged explicitly; there is no random-access claim.

The simpler #45 control stores one complete tier as the shared base, uses the
same CDC partition to find exact COPY ranges, and stores unmatched target bytes
verbatim in compressed residual streams. All three base choices are costed and
retained. This is a bounded COPY/literal baseline, not a VCDIFF/BSDiff result or
a solution to the broader #45 issue. The size gate for #379 uses range-sharing
candidates; the one-base control is a competing simpler representation.
Range inventory fields are marked inapplicable for this control; its pool,
residual, path, complete-size and selected-target costs are still fully reported.

The reported **estimated complete size** is the real existing launcher size plus
the actual container size plus a 64 KiB incremental decoder/stub reserve. The
launcher cannot execute this container. The reserve is a declared estimate,
with 0 and 256 KiB sensitivity columns, not a measured decoder build. A zero-cost
decoder is an optimistic scenario, not a proven global lower bound on all
possible sharing algorithms.

The opportunity gate is at least **5% net complete-size savings in at least three
of the four held-out families, in both stripping profiles**. A family only passes
when one range representation clears the size threshold and the following
selected-target estimates for every tier. No family may be discarded after
results. If even the zero-incremental-decoder scenario misses this size gate,
stop #379 graph implementation on this candidate space. If the 64 KiB costed
gate clears, compare the simpler one-base frontier before allocating a decoder
prototype. Neither outcome completes #47/#379 generally or changes defaults.

The selected-target buffer estimate is decoded shared pool + target residual +
original output + raw path + **32 MiB codec reserve**, capped at **256 MiB**. This
is a logical buffer/reserve scenario, not a measured RSS bound. Packaging input
snapshots, chunk search, encoder memory and retained allocations are not covered.
No runtime memory policy follows from it.

Cold-path modeled work charges selected encoded bytes read, all selected decoded
bytes, hashes of pool/residual/path/output, a copy of the full output and one
output write. The scenario uses 500 MiB/s for reads, decode and hashing, and
1000 MiB/s for copying/writing. The screening budget is at most **10% modeled
increase** against the selected frame of the stronger complete-artifact control.
These are declared illustrative rates, not measurements or predictive startup
claims. Launcher/Go bootstrap, page cache, syscalls, contention and initialization
can dominate. **The real cold-start and memory gate remains unresolved until a
bounded decoder exists and is independently qualified.** Warm verified-cache
execution skips reconstruction and is not an expected speed beneficiary.

Bounds: 96 MiB per ELF, three tiers/288 MiB per group, 1 GiB whole corpus,
524288 chunk occurrences, 16 MiB raw path, 64 KiB header and 1152 MiB container.
The input bound includes the pinned unstripped Task binary (73,996,007 bytes),
whose size was known during corpus preparation before the protocol freeze.
Inputs are nonempty regular
descriptor-validated files with exact pinned hashes. Header, frame, traversal,
range, expansion, checksum and output limits reject malformed representations.

## Reproduce

Use Go 1.27.3 and pinned Task. Corpus creation is outside timing:

```bash
task research-heldout-corpus CORPUS_OUTPUT=/new/absolute/corpus CORPUS_ARCH=amd64
task research-47-build
task research-47 MANIFEST=/absolute/corpus/manifest.json \
  STUB=/absolute/corpus/stub-full OUTPUT=/new/absolute/report-directory
```

The output contains one report, actual baseline fat artifacts and actual custom
research containers. The report pins the producer revision, manifest/source/module
identities, Go/dependency version, stub and corpus hashes, all attempts and the
fixed cost assumptions. No performance timer is part of this first size screen.
Native execution, startup quantiles, packaging RSS, ARM64 and real decoder size
remain separate evidence. Unit tests use synthetic inputs; a successful unit
matrix is not a held-out measurement.
