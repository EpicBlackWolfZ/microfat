# Held-out exact-sharing screen — 2026-10-23

**There is useful size opportunity, but no material advantage for a range graph
over a simpler one-base representation.** CDC clears the prespecified 5% estimated
complete-size margin for all four held-out families in both stripping profiles.
The current offline representations fail the illustrative cold-work budget.
Keep #379 gated; compare and simplify the #45 selected-target path before any
production decoder or format proposal. This does not establish startup benefits.

## Source, inputs and evidence

The protocol and implementation were committed **before the corpus screen** at
`e32e294ece6b7bc17eaa832ec4eac811fd4069d9`; the pinned common corpus recipe is
`f87db35fdf9fc7f51d262bd15634a3dd19c01a5e`. Go 1.27.3 and
klauspost/compress v1.20.3, native Linux amd64, Zstd/default, three GOAMD64 tiers,
CGO disabled, trimpath and buildvcs disabled. Four pinned module families:
Fortio v1.76.0, Task v3.54.0, actionlint v1.7.12 and govulncheck v1.9.0. The
24 stripped/unstripped ELFs total 650,430,867 bytes. These families were absent
from the earlier demo/microfat screens; they remain a convenience corpus of Go
programs sharing a compiler/standard library, not a random application sample.

The measured launcher is 3,989,628 bytes. The binary, launcher, manifest, modules,
input hashes, source checksums and exact environment are recorded in
`evidence/provenance.json` and the full `evidence/report.json.gz`. The complete
baseline fat artifacts and custom containers are retained locally under
`.work/research47/heldout-amd64/`; executables are not committed. The raw report
retains absolute paths for that original owned tree. Reproduction uses the shared
Task corpus recipe and the commands in the protocol README.

This was a **size-only** screen with GOMAXPROCS=4 and GOMEMLIMIT=2GiB. There are
no pack/decode timers, CPU measurements, RSS measurements, launch samples or p99
claims. Other development activity is immaterial to deterministic byte counts.
The memory soft limit is not evidence of a hard RSS bound.

## Complete size results

All 16 actual Format v2 baseline artifacts and all 56 custom containers passed
verification: **48 baseline and 168 custom-container payload round trips**, each
with exact source bytes and SHA-256. There were zero group, baseline or candidate
errors, and no family/profile was omitted. All fixed512 partitions remained
inside the occurrence cap, including the large unstripped Task inputs.

An independent audit used a separate Python standard-library parser and the
system libzstd, without calling the Go encoder or decoder. It rehashed all 24
source ELFs and all 72 actual output files, checked actual compact indexes and
trailers, exact stub boundaries, dictionary IDs/hashes, custom headers, pool and
residual integrity, path bounds and complete cost arithmetic. **All 216 independent
decoded byte comparisons passed.** Its result is retained in
`evidence/independent-audit.json`. This is an independent reconstruction check,
not execution or a performance measurement.

The production no-dictionary control is smaller than the current-dictionary
control in all eight groups. Dictionaries add 59,195–94,889 bytes here. This is
Zstd/default on these exact inputs; it is not a matched repeat of #161's earlier
Zstd/better corpus or a general statement about dictionaries.

Below, controls are **actual complete fat artifacts**. Candidate totals are the
real existing launcher + actual custom container + an **estimated 64 KiB added
decoder**. They are costed representation estimates, not runnable fat artifacts.
CDC is the smallest of the four range partitions in every group. All three base
choices were retained; v2 is the smallest one-base choice in every group.

| Family | Profile | Control bytes | CDC estimated complete bytes | CDC saving | One-base estimated complete bytes | One-base minus CDC |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| actionlint | stripped | 11,439,908 | 10,566,040 | 7.639% | 10,556,815 | -9,225 B |
| actionlint | unstripped | 18,399,190 | 17,444,885 | 5.187% | 17,443,657 | -1,228 B |
| Fortio | stripped | 26,954,081 | 23,404,376 | 13.169% | 23,367,560 | -36,816 B |
| Fortio | unstripped | 47,848,736 | 43,977,739 | 8.090% | 43,951,135 | -26,604 B |
| govulncheck | stripped | 17,887,461 | 16,096,080 | 10.015% | 16,081,352 | -14,728 B |
| govulncheck | unstripped | 30,999,148 | 28,962,280 | 6.571% | 28,959,971 | -2,309 B |
| Task | stripped | 52,110,382 | 44,108,839 | 15.355% | 44,136,560 | +27,721 B |
| Task | unstripped | 98,063,687 | 89,159,314 | 9.080% | 89,219,758 | +60,444 B |

The one-base control is smaller in six groups. Range sharing wins the two Task
groups by just 27,721/60,444 bytes; every difference is below 0.15% of the
corresponding complete representation. Both controls use the same decoder-size
reserve. Their actual implementations could have different code costs, which
remain unmeasured. There is no demonstrated reward for graph complexity here.

Whole-section sharing loses complete bytes in all groups. Fixed partitions save
roughly 1.3–6.0%, but do not clear the family gate in both profiles. CDC finds
substantially more exact universal/subset sharing than fixed alignment; this is
evidence that the earlier #161 fixed-window detector missed a different candidate
space. It does not revive the tested dictionary selectors or establish that
gross duplicate coverage equals compressed savings.

## Gate and selected-target costs

The 64 KiB costed **size** gate passes **4/4 families in both profiles**. The
zero-added-decoder sensitivity also passes 4/4; the larger 256 KiB reserve passes
3/4, with actionlint/unstripped below 5%. The positive byte opportunity is robust
enough for a simpler bounded comparison, without treating the reserve as a real
decoder measurement or the zero-reserve scenario as a global lower bound.

For the winning range candidates, the largest selected-target buffer/reserve
scenario per group is 44.89–174.93 MiB, within the declared 256 MiB scenario. These
are arithmetic buffer estimates including a chosen 32 MiB codec reserve. They
exclude packaging snapshots/search and retained allocations, and do not bound
actual decoder or process RSS.

Every winning range candidate fails the 10% **modeled** cold-work margin: the
worst target per group has 53.72–59.51% more modeled work. A selected target must
decode the whole shared pool, hash pool/residual/path plus the reconstructed
output, and copy the output before writing. The simpler one-base representation
also hashes extra material. The assumed read/decode/hash/copy rates and additive
model are not measurements, a startup bound or a validated prediction. Some work
could be fused or avoided in a different, carefully verified decoder.

Thus the combined modeled screen passes **0/4 families** while the size gate
passes **4/4**. Those are distinct outcomes. Actual cold-start, cache/memfd,
workspace, RSS and tail qualification remains unresolved; no runtime decoder
or production policy follows from this result.

## Next decision

Keep the cheap exact-sharing diagnostic and its negative controls. Park #379's
graph representation: the simpler one-base frontier already matches it here.
If size savings justify more work, the next bounded #45 experiment should prove
selected-target reconstruction costs, exact corruption/expansion rejection and
real decoder size before full launch qualification. Compare against stronger
independent codec/level choices under declared pack/decode budgets. Retain the
existing production format and defaults throughout that comparison.

This evidence covers amd64 v1/v2/v3 file reconstruction only. No payload was
executed by this screen, and there is no ARM64, v4, cross-toolchain, future-version,
service-latency or population-p99 result.
