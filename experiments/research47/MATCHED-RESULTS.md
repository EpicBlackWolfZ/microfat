# Matched inter-variant deduplication evaluation

Retain independent Format v2 frames for v0.4.0. On a native amd64 Linux host, two fixed representations cleared
the artifact size gate against matched production controls. The measured reconstruction stages exceeded the cold-path
budget, and the one-base representation failed under the 256 MiB memory ceiling for unstripped Task. This
disposition covers the tested representations and the v0.4.0 release. See [#410][issue].

## Complete artifacts

The [frozen protocol](README.md#matched-comparison-protocol) uses four pinned Go application families, stripped
and unstripped, at three ISA tiers. The source was `b1b3f9fb09ede6569bfa4371424bf076dbb0b50a`, a clean merge
of main `7f118cf` and the original matched screen `2d69f3e`. Go 1.27.4, klauspost/compress 1.20.4,
and Task 3.54.0 produced the artifacts. The same 3,919,996-byte full launcher is charged to both arms.
Each candidate total includes the actual pool, literals, paths, framing and metadata, plus a 64 KiB incremental
decoder allowance. The complete production controls include actual stub, dictionary, frames, index and trailer.

The table shows the two strongest fixed representations and the smaller real production artifact from the
`best` no-dictionary and `size` controls. The full report contains all seven tested candidates and both
encoding levels. Savings are measured against the complete control bytes.

| Family | Profile | Strongest best control | Control bytes | One-base v2 savings | Shared Gear CDC savings |
| --- | --- | --- | ---: | ---: | ---: |
| actionlint | stripped | best, no dictionary | 10,835,506 | 7.69% | 7.59% |
| actionlint | unstripped | best, no dictionary | 17,689,344 | 4.85% | 4.79% |
| Fortio | stripped | best, no dictionary | 24,854,887 | 13.44% | 13.36% |
| Fortio | unstripped | best, no dictionary | 45,674,631 | 7.92% | 8.06% |
| govulncheck | stripped | size | 16,705,379 | 10.02% | 10.00% |
| govulncheck | unstripped | best, no dictionary | 29,779,541 | 6.41% | 6.55% |
| Task | stripped | best, no dictionary | 46,911,596 | 15.38% | 15.34% |
| Task | unstripped | best, no dictionary | 92,583,083 | 8.59% | 8.62% |

The frozen 5% threshold requires the same representation to clear it for both stripping profiles in at
least three of the four families. At both matched levels, the maximum is three families with the 64 KiB allowance
and four with zero decoder growth. Neither level qualifies any family when the prespecified modeled workspace and
cold-path budgets are applied. At matched `best`, the worst target in each family/profile raised modeled cold-path
work by 54–60% for shared CDC and 114–123% for one-base v2. The 64 KiB allowance remains an estimate. No runnable
production launcher was built because the tested candidates missed the runtime gate even without charging startup
or actual added launcher bytes.

The size screen completed all 33 production controls and 154 research containers, including the three synthetic
boundary groups. An independent parser and system libzstd 1.5.7 checked the actual bytes and reproduced all
561 payloads byte-for-byte. The previous [Zstd/default comparison](RESULTS.md) remains historical evidence.
The higher-level re-encoding established size headroom that the earlier comparison against stronger controls
did not establish.

## Local Pack and cold reconstruction diagnostics

The [diagnostic protocol](evidence/matched-20261115/diagnostic-protocol.md) fixed all four families, both
profiles, both selected representations and the scope budgets before timing. The benchmark host used Linux
7.3.4, native amd64, an ext4 work area and fresh cgroups with a four-core quota and swap disabled.
All 56 packing observations reproduced the corresponding complete output bytes and SHA-256 values.
At matched `better`, the diagnostic build wall-time ratios were 1.24–1.65× for one-base and 1.25–1.89× for CDC.
At matched `best`, across both production controls, they were 1.88–2.33× and 1.67–2.17× respectively.
For example, the Task unstripped `best` production control took 2.91 s wall, 8.69 s process CPU and 717 MiB
cgroup peak. Its one-base candidate took 6.28 s wall, 6.56 s CPU and 968 MiB cgroup peak. The CDC candidate took
6.22 s wall, 6.52 s CPU and 1,025 MiB cgroup peak. Candidate builds write experimental containers. The production
control writes a full artifact with its normal atomic publication. Each of these values comes from one build per
cell, so they describe this cohort only.

The cold diagnostic reconstructed the same non-base `v3` target and verified its bytes before sealing a new memfd
or atomically materializing a new private cache entry. Seven rotated blocks per family/profile/mode produced 336
attempts. All 112 production controls and 112 CDC attempts succeeded. One-base succeeded in 98 of 112 attempts.
All fourteen one-base failures occurred on unstripped Task at 256 MiB, with thirteen cgroup OOM kills and one
terminated scope where the final counters were unavailable. The production controls succeeded in each of the
same blocks.

| Family | Profile | One-base memfd | CDC memfd | One-base cache | CDC cache |
| --- | --- | ---: | ---: | ---: | ---: |
| actionlint | stripped | 2.40× | 1.79× | 1.90× | 1.50× |
| actionlint | unstripped | 2.82× | 2.10× | 1.94× | 1.46× |
| Fortio | stripped | 3.04× | 2.19× | 2.05× | 1.58× |
| Fortio | unstripped | 3.01× | 2.14× | 2.27× | 1.68× |
| govulncheck | stripped | 2.20× | 1.62× | 1.65× | 1.31× |
| govulncheck | unstripped | 3.16× | 2.27× | 2.06× | 1.64× |
| Task | stripped | 3.18× | 2.27× | 2.44× | 1.91× |
| Task | unstripped | 0/7 completed | 2.77× | 0/7 completed | 1.86× |

Each ratio is the median paired wall-time ratio of the selected-target reconstruction and materialization stage.
For unstripped Task, CDC cgroup peaks reached 256 MiB in memfd and were within 0.04 MiB of the cap in cache.
The strongest independent control peaked below 98 MiB in both modes. These scopes report cgroup peaks including
charged file pages. Process RSS and process CPU are recorded separately. The timings exclude actual application
exec and launcher bootstrap, use unpurged input pages and apply to this local amd64 diagnostic only.

The candidates provide meaningful storage savings, but neither clears the 10% cold-stage budget. Warm verified
hits already consume materialized payloads. This evaluation leaves the deployed execution paths and format in
place. A later candidate would need a bounded reconstruction path with fresh matched measurements and the
production corruption, control and descriptor-security qualification required by [#410][issue].

## Evidence

The [checksummed reports](evidence/matched-20261115/SHA256SUMS) retain all representation, control, independent
audit and cost outcomes. The size report replaces machine-specific path prefixes for portability. The
[original report and campaign digests](evidence/matched-20261115/RAW_SHA256SUMS) identify the retained local
inputs, worker, protocol and raw results. To inspect the per-attempt costs and exactness, decompress
`cost-diagnostic.json.gz` and `independent-audit.json.gz` with `gzip -dc`.

[issue]: https://github.com/EpicBlackWolfZ/microfat/issues/410
