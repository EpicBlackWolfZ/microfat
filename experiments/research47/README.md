# Exact-sharing size screen (#410)

This offline screen reconstructs exact ELF bytes and compares complete container
sizes. It exercises the #410 size gate ahead of runtime qualification. The
[original #47 results](RESULTS.md) retain their Zstd/default protocol and output;
they used the earlier controls.

## Matched comparison protocol

Use the checksum-pinned [shared corpus](../codecpolicy/README.md) with Fortio,
Task, actionlint and govulncheck, each stripped and unstripped at three native ISA
tiers. The three synthetic boundary ELFs are exactness controls. Their byte
counts and verification outcomes are retained separately from the four-family
gate.

Each group gets three actual Format v2 artifacts from the current production
packer, using the same full launcher and an epoch of zero. The matched control
for `better` is the production `balanced` profile. The matched control for
`best` is the smaller of the production `size` profile with its automatic
dictionary and the explicit `zstd:best` artifact without a dictionary. These
controls reproduce the settings from the
[#42 study](../../docs/codec-policy-results.md#complete-artifact-bytes).
#42's published byte totals use its own pinned build and source revision.
The report identifies the current corpus and stub and keeps every generated
control, including its compressed frame and complete artifact hashes. Candidate
pool, literal residual, and compressed path use the corresponding `better` or
`best` level. The two levels are evaluated independently.

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

The size gate is at least **5% complete-size savings** for one fixed
representation and base choice in at least **three of the four** held-out
families, in both stripping profiles. Meet this gate at the matched `best`
level before proposing any runtime prototype for #410. Report both levels and
every group, including lost-size cases and failures. The reports also retain the
zero- and 256 KiB incremental decoder scenarios. The 64 KiB primary reserve
remains an estimate inherited from the original screen. A release decision
requires measured or defensibly upper-bounded growth in each launcher profile.

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

Use the Go patch pinned in `go.mod` and pinned Task. Build the corpus in a
clean committed checkout and place output outside tracked paths.

```bash
task research-47-corpus CODEC_CORPUS=/new/absolute/corpus CODEC_ARCH=amd64
task research-47-build
task research-47 MANIFEST=/new/absolute/corpus/manifest.json \
  STUB=/new/absolute/corpus/stub-full OUTPUT=/new/absolute/report-directory
```

The report records the producer and corpus revisions, Go and codec versions,
full stub and variant hashes, fixed cost assumptions and each attempt. A
repeat on native arm64 uses `CODEC_ARCH=arm64`. Keep each report, actual baseline
fat binaries and research containers in their own output tree.

Runtime qualification requires a separate frozen protocol and a measured
launcher baseline. The size screen does not collect Pack wall/CPU or cgroup
peak, build a runnable launcher, or time cold memfd, cold cache and warm cache
paths. Retain independent variant frames pending the #410 complete-cost review.

The [matched evaluation](MATCHED-RESULTS.md) records the v0.4.0 retention disposition, including the
complete-artifact size results and the measured local materialization costs.
