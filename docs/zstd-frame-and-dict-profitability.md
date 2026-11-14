# Zstd frame/window tuning and opt-in dictionary profitability

Retain the existing deterministic `ZstdCodec` frame/window defaults (`<= 8 MiB`
sliding window on streamed frames, single-segment on sub-128-KiB inputs, and
raw `opts.CompressionLevel` dictionary training) and ship explicit opt-in
complete-byte dictionary profitability gating (`--dict-profitable`, manifest
`compression.dict_profitable`, `pack.Options.DictProfitable`). This is the
bounded `v0.4.0` evaluation and deliverable for
[#409](https://github.com/EpicBlackWolfZ/microfat/issues/409).

Known-size Zstd frame/window alternatives (`ResetContentSize`, `32 MiB`/`64 MiB`
windows, and `EncodeAll` single-segment frames) and `"best"` trainer-level
overrides do not clear the [#42](codec-policy-results.md) replacement gates:
larger windows save at most `0.04%–0.75%` on the largest multi-variant Go
binaries while adding bytes on smaller groups, increasing `Pack` latency by up
to `11%`, and raising Zstd decoder window requirements from `8 MiB` to
`32–64 MiB`. By contrast, opt-in `--dict-profitable` eliminates `+5,987` to
`+82,994` wasted dictionary bytes across all seven unprofitable Go groups,
retains the `-23,141 B` win on `govulncheck / stripped` and the `-195,850` to
`-195,925 B` wins on the `512 KiB` boundary fixtures, speeds up cold `memfd`
and `cold-cache` startup in `7/8` real Go groups relative to unconditional
`size` dictionaries (`0.6650–0.85x` median paired wall ratio), and preserves
100% wire compatibility with existing `microfat-stub` binaries and standard
Zstandard decoders. Because evaluating both candidate sets approximately doubles
`Pack` wall and CPU cost (`1.82x–2.05x`), `--dict-profitable` remains opt-in
rather than replacing `size`'s default behavior.

## Protocol and corpus identity

Measurements used the [pinned four-family Go corpus](../experiments/codecpolicy/README.md)
built cleanly from `main` `0a51ef744082a53890f7a1ef21f8ecb25a11d424` with Go
`1.27.3`, `CGO_ENABLED=0`, `GOMAXPROCS=4`, `SOURCE_DATE_EPOCH=0`, Format v2, and
isolated telemetry-off configuration (`XDG_CONFIG_HOME` mode `off 1970-01-01\n`)
on a shared native `linux/amd64` Ryzen 9 3950X host (`32` logical CPUs, Linux
`7.2.11-200.nobara.fc44`):

- **Corpus**: Fortio `1.76.0`, Task `3.54.0`, actionlint `1.7.12`, and
  govulncheck `1.9.0` (`stripped` and `unstripped`, `amd64` `v1`/`v2`/`v3`) plus
  three static `512 KiB` syscall boundary ELFs (`524,287`, `524,288`, and
  `524,289` bytes). Source lock SHA-256:
  `872a8fe7b2a481ff89edcf71f5945c8b98d79dc82d161066f986ab48511ffcfd`.
- **Wire-compatibility proof**: `Pack` artifacts produced by the updated CLI and
  experiment worker were verified with `pack.VerifyBinary`, decoded directly via
  `klauspost/compress/zstd` under `DefaultDecoderMaxMemory` (`1 GiB`) and
  `DefaultDecoderMaxWindow` (`128 MiB`), and executed through unmodified
  `stub-full` (`4,006,012 B`) and `stub-minimal` (`3,768,444 B`) binaries built
  from `0a51ef744082a53890f7a1ef21f8ecb25a11d424`.
- **Reproducibility and completeness**: All `396` timed `Pack` runs (`11` groups
  x `12` settings x `3` rounds) reproduced identical per-cell SHA-256 digests
  (`0` non-reproducible cells). All `9,240` timed launches (`11` groups x `2`
  stubs x `3` execution modes x `7` launch settings x `20` paired rounds) exited
  `0` with exact native reference `stdout`/`stderr` parity.

## Complete artifact bytes and frame window bounds

All byte counts below are complete Format v2 `full`-stub executables (launcher
stub + optional dictionary + compressed variant frames + binary index + 56-byte
trailer):

| Group | `size` (auto dict) | `size` (`trainer=best`) | `best` / no dict | `size --dict-profitable` | `best` (`kcs`) | `best` (`32 MiB` win) | `best` (`64 MiB` win) | `best` (`EncodeAll`) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `actionlint / stripped` | 10,925,417 | 10,925,409 | 10,919,430 | **10,919,430** *(no dict)* | 10,919,442 | 10,919,430 | 10,919,430 | 10,919,478 |
| `actionlint / unstripped` | 17,782,712 | 17,782,704 | 17,771,806 | **17,771,806** *(no dict)* | 17,771,818 | 17,771,833 | 17,771,833 | 17,771,854 |
| `fortio / stripped` | 24,984,806 | 24,984,800 | 24,944,204 | **24,944,204** *(no dict)* | 24,944,216 | 24,934,488 | 24,934,488 | 24,944,252 |
| `fortio / unstripped` | 45,819,672 | 45,819,665 | 45,760,158 | **45,760,158** *(no dict)* | 45,760,170 | 45,509,569 | 45,509,569 | 45,760,206 |
| `govulncheck / stripped` | 16,791,407 | 16,791,406 | 16,814,548 | **16,791,407** *(98,443 B dict)* | 16,814,560 | 16,814,586 | 16,814,586 | 16,814,596 |
| `govulncheck / unstripped` | 29,890,037 | 29,890,027 | 29,864,452 | **29,864,452** *(no dict)* | 29,864,464 | 29,742,426 | 29,742,426 | 29,864,500 |
| `task / stripped` | 47,074,397 | 47,074,386 | 46,994,748 | **46,994,748** *(no dict)* | 46,994,760 | 46,701,647 | 46,701,722 | 46,994,796 |
| `task / unstripped` | 92,748,243 | 92,748,236 | 92,665,249 | **92,665,249** *(no dict)* | 92,665,261 | 92,333,292 | 91,974,524 | 92,665,297 |
| `boundary / below` (`524,287 B`) | 5,383,495 | 5,383,498 | 5,579,345 | **5,383,495** *(98,404 B dict)* | 5,579,357 | 5,579,345 | 5,579,345 | 5,579,393 |
| `boundary / at` (`524,288 B`) | 5,383,432 | 5,383,432 | 5,579,357 | **5,383,432** *(98,404 B dict)* | 5,579,369 | 5,579,357 | 5,579,357 | 5,579,396 |
| `boundary / above` (`524,289 B`) | 5,383,435 | 5,383,435 | 5,579,360 | **5,383,435** *(98,404 B dict)* | 5,579,372 | 5,579,360 | 5,579,360 | 5,579,408 |

On `balanced` (`zstd:better`), `--dict-profitable` shows the same zero-loss
property:

| Group | `balanced` | `balanced` (forced `--dict`) | `balanced --dict-profitable` | `balanced` (`32 MiB` win) |
| --- | ---: | ---: | ---: | ---: |
| `actionlint / stripped` | 11,296,939 | 11,295,594 | **11,295,594** *(98,445 B dict, −1,345 B)* | 11,296,939 |
| `actionlint / unstripped` | 18,186,398 | 18,186,946 | **18,186,398** *(no dict)* | 18,186,397 |
| `fortio / stripped` | 26,145,835 | 26,195,151 | **26,145,835** *(no dict)* | 26,139,044 |
| `fortio / unstripped` | 47,029,093 | 47,092,351 | **47,029,093** *(no dict)* | 47,000,687 |
| `govulncheck / stripped` | 17,544,400 | 17,524,701 | **17,524,701** *(98,443 B dict, −19,699 B)* | 17,544,400 |
| `govulncheck / unstripped` | 30,640,933 | 30,666,699 | **30,640,933** *(no dict)* | 30,593,935 |
| `task / stripped` | 49,810,747 | 49,864,210 | **49,810,747** *(no dict)* | 49,790,239 |
| `task / unstripped` | 95,641,044 | 95,679,076 | **95,641,044** *(no dict)* | 95,643,375 |

### Frame/window and trainer observations

1. **Trainer-level control (`size` vs `trainer=best`)**: Passing `"best"` to
   `codec.TrainDictionary` instead of raw `""` (`SpeedDefault`) changes complete
   artifact bytes by only `-1` to `-11 B` on real Go binaries (`+3 B` on
   `boundary / below`). The `7/8` unprofitable automatic dictionaries in `#42`
   are caused by `8 MiB` intra-frame sliding windows already capturing repeated
   Go runtime/symbol prefixes, not by trainer-level mismatch. Existing
   `TestPackSizePolicyMatchedDictionaryControls` semantics are retained.
2. **Known content size (`ResetContentSize`)**: Shrinks declared Zstd frame
   `WindowSize` on the `512 KiB` boundary fixtures (`524,288 B` on `below` and
   `1,048,576 B` on `at`/`above`, vs `8,388,608 B` streamed), while adding
   `+12 B` (`+4 B` Frame_Content_Size per variant) across all 11 groups and
   leaving `>= 8 MiB` Go binaries at the same `8,388,608 B` window.
3. **Larger encoder windows (`32 MiB` / `64 MiB`)**: Remain within
   `DefaultDecoderMaxWindow` (`128 MiB`), but raise decoder sliding-window memory
   from `8 MiB` to `32 MiB` / `64 MiB`, add bytes on `actionlint / unstripped`
   (`+27 B` at `best`), `govulncheck / stripped` (`+38 B` at `best`), and
   `task / unstripped` (`+2,331 B` at `better` `32 MiB`), and save at most
   `0.36%` (`32 MiB`) to `0.75%` (`64 MiB`) on `task / unstripped` — below the
   1% `size` and 5% `balanced` replacement thresholds.
4. **Single-shot `EncodeAll` (`WithSingleSegment`)**: Adds `+39` to `+48 B`
   across all 11 groups with no compression or cold-launch advantage, while
   preserving default `ZstdCodec`'s automatic single-segment path for inputs
   strictly smaller than `128 KiB`.

## `Pack` wall, CPU, and `wait4` RSS costs

Each cell reports **median wall ms / median user+sys CPU ms / max observed
`wait4` RSS MiB** over three fresh child processes (`GOMAXPROCS=4`; parallel
variant encoding active across the three variants). As documented in
[codec-policy-results.md](codec-policy-results.md#packing-cost-and-unresolved-memory-attribution),
`wait4` `Maxrss` reflects the inherited parent address-space high-water floor
under `CLONE_VFORK | CLONE_VM` (`~598–606 MiB` from the runner binary) and is not
an isolated encoder allocation reading.

| Group | `size` | `size --dict-profitable` | `best` / no dict | `best` (`32 MiB`) | `best` (`64 MiB`) | `balanced` | `balanced --dict-profitable` |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `actionlint / stripped` | 454.91 / 1311.89 / 598.28 | 916.68 / 2719.26 / 598.28 | 430.14 / 1268.27 / 598.28 | 444.68 / 1279.42 / 598.28 | 471.41 / 1340.04 / 598.28 | 130.86 / 364.83 / 598.28 | 263.91 / 774.65 / 598.28 |
| `actionlint / unstripped` | 500.44 / 1434.44 / 598.28 | 1009.12 / 2978.74 / 598.28 | 487.97 / 1415.51 / 598.28 | 540.24 / 1534.05 / 598.28 | 509.37 / 1437.43 / 598.28 | 152.35 / 424.13 / 598.28 | 333.73 / 983.62 / 598.28 |
| `fortio / stripped` | 1453.26 / 4312.96 / 598.28 | 2786.23 / 8416.70 / 598.28 | 1421.12 / 4253.48 / 598.28 | 1501.05 / 4377.88 / 598.28 | 1434.72 / 4184.01 / 598.28 | 492.79 / 1469.83 / 598.28 | 857.56 / 2608.72 / 598.28 |
| `fortio / unstripped` | 1655.00 / 4894.20 / 598.28 | 3148.92 / 9465.86 / 598.28 | 1595.53 / 4746.95 / 598.28 | 1721.96 / 4949.19 / 598.28 | 1650.73 / 4759.34 / 598.28 | 528.71 / 1555.58 / 598.28 | 968.78 / 2973.43 / 598.28 |
| `govulncheck / stripped` | 901.13 / 2640.35 / 598.28 | 1643.75 / 4904.68 / 598.28 | 857.66 / 2565.97 / 598.28 | 867.35 / 2503.46 / 598.28 | 854.21 / 2478.03 / 598.28 | 256.97 / 759.53 / 598.28 | 519.68 / 1557.73 / 598.28 |
| `govulncheck / unstripped` | 924.51 / 2725.02 / 598.28 | 1839.73 / 5528.50 / 598.28 | 935.38 / 2758.36 / 598.28 | 965.47 / 2765.91 / 598.28 | 967.13 / 2776.38 / 598.28 | 310.51 / 886.75 / 598.28 | 550.40 / 1672.46 / 598.28 |
| `task / stripped` | 2997.98 / 8977.20 / 598.28 | 5954.92 / 17857.20 / 645.05 | 3008.78 / 8958.00 / 598.28 | 3342.83 / 9731.08 / 598.28 | 3481.57 / 10151.97 / 598.28 | 999.41 / 3027.92 / 598.28 | 1844.91 / 5724.84 / 598.28 |
| `task / unstripped` | 3507.78 / 10419.73 / 598.28 | 7184.48 / 21513.78 / 600.98 | 3693.01 / 11005.24 / 598.28 | 4104.19 / 11941.04 / 598.28 | 4038.95 / 11747.31 / 598.28 | 1128.81 / 3334.96 / 605.63 | 2159.90 / 6662.68 / 605.63 |
| `boundary / below` | 53.85 / 106.00 / 605.63 | 66.61 / 142.45 / 605.63 | 23.03 / 38.24 / 605.63 | 23.36 / 35.13 / 605.63 | 22.58 / 36.39 / 605.63 | 17.16 / 17.19 / 605.63 | 33.30 / 47.81 / 605.63 |
| `boundary / at` | 50.75 / 102.62 / 605.63 | 62.99 / 135.87 / 605.63 | 22.80 / 36.76 / 605.63 | 23.44 / 36.58 / 605.63 | 22.95 / 35.92 / 605.63 | 17.25 / 16.27 / 605.63 | 30.14 / 44.15 / 605.63 |
| `boundary / above` | 52.73 / 101.14 / 605.63 | 63.85 / 139.95 / 605.63 | 23.71 / 40.47 / 605.63 | 22.65 / 37.75 / 605.63 | 26.92 / 43.62 / 605.63 | 16.99 / 17.38 / 605.63 | 34.66 / 53.50 / 605.63 |

`assembleProfitableArtifact` stages each Zstandard variant once without a
dictionary and once with the candidate dictionary inside `MaxDictProfitabilitySpoolBytes`
(`2 GiB`), encodes non-dictionary (`none`, `lz4`) override variants only once,
computes exact complete candidate bytes (including the `FormatVersion2` binary
index and 56-byte trailer), copies the winning staged frames
without a third compression pass, and removes all staged files before returning.

## Paired launch completion intervals

Each row summarizes the eight real Go groups (`20` paired rounds per cell, `2000`
bootstrap resamples with PCG seed `(42, 42)`, 95% percentile endpoints
`floor(p * 1999)`). Cells are classified as **Equivalent** when the 95% ratio
interval lies wholly inside `[0.95, 1.05]`, **Faster** when `Upper95 < 0.95`,
**Slower** when `Lower95 > 1.05`, and **Inconclusive** otherwise:

| Candidate vs anchor | Stub | Mode | Median paired ratio range | Equivalent | Faster | Slower | Inconclusive |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| `size --dict-profitable` vs `size` | `full` | `memfd` | 0.6650–1.0589 | 0 | 7 | 0 | 1 |
| `size --dict-profitable` vs `size` | `full` | `cold-cache` | 0.7955–1.0263 | 0 | 7 | 0 | 1 |
| `size --dict-profitable` vs `size` | `full` | `warm-cache` | 0.9796–1.0231 | 8 | 0 | 0 | 0 |
| `size --dict-profitable` vs `size` | `minimal` | `memfd` | 0.7008–1.0565 | 0 | 7 | 0 | 1 |
| `size --dict-profitable` vs `size` | `minimal` | `cold-cache` | 0.7517–0.9855 | 0 | 7 | 0 | 1 |
| `size --dict-profitable` vs `size` | `minimal` | `warm-cache` | 0.9775–1.0119 | 8 | 0 | 0 | 0 |
| `size --dict-profitable` vs `best / no dict` | `full` | `memfd` | 0.9253–1.3967 | 0 | 0 | 1 | 7 |
| `size --dict-profitable` vs `best / no dict` | `full` | `cold-cache` | 0.9785–1.3330 | 2 | 0 | 1 | 5 |
| `size --dict-profitable` vs `best / no dict` | `full` | `warm-cache` | 0.9909–1.0106 | 8 | 0 | 0 | 0 |
| `size --dict-profitable` vs `best / no dict` | `minimal` | `memfd` | 0.9481–1.4487 | 0 | 0 | 1 | 7 |
| `size --dict-profitable` vs `best / no dict` | `minimal` | `cold-cache` | 0.9229–1.2037 | 1 | 0 | 1 | 6 |
| `size --dict-profitable` vs `best / no dict` | `minimal` | `warm-cache` | 0.9788–1.0081 | 8 | 0 | 0 | 0 |
| `best (32 MiB)` vs `best / no dict` | `full` | `memfd` | 0.9376–1.1099 | 0 | 0 | 0 | 8 |
| `best (32 MiB)` vs `best / no dict` | `full` | `cold-cache` | 0.9749–1.0322 | 2 | 0 | 0 | 6 |
| `best (32 MiB)` vs `best / no dict` | `full` | `warm-cache` | 0.9909–1.0128 | 8 | 0 | 0 | 0 |
| `best (32 MiB)` vs `best / no dict` | `minimal` | `memfd` | 0.9664–1.0906 | 0 | 0 | 0 | 8 |
| `best (32 MiB)` vs `best / no dict` | `minimal` | `cold-cache` | 0.9531–1.0506 | 1 | 0 | 0 | 7 |
| `best (32 MiB)` vs `best / no dict` | `minimal` | `warm-cache` | 0.9826–1.0024 | 8 | 0 | 0 | 0 |
| `best (EncodeAll)` vs `best / no dict` | `full` | `memfd` | 0.9386–1.0835 | 1 | 0 | 0 | 7 |
| `best (EncodeAll)` vs `best / no dict` | `full` | `cold-cache` | 0.9565–1.0401 | 1 | 0 | 0 | 7 |
| `best (EncodeAll)` vs `best / no dict` | `full` | `warm-cache` | 0.9873–1.0164 | 8 | 0 | 0 | 0 |
| `best (EncodeAll)` vs `best / no dict` | `minimal` | `memfd` | 0.9475–1.1033 | 0 | 0 | 0 | 8 |
| `best (EncodeAll)` vs `best / no dict` | `minimal` | `cold-cache` | 0.9500–1.0717 | 1 | 0 | 0 | 7 |
| `best (EncodeAll)` vs `best / no dict` | `minimal` | `warm-cache` | 0.9785–1.0089 | 8 | 0 | 0 | 0 |
| `balanced --dict-profitable` vs `balanced` | `full` | `memfd` | 0.9307–1.4822 | 0 | 0 | 2 | 6 |
| `balanced --dict-profitable` vs `balanced` | `full` | `cold-cache` | 0.9833–1.2329 | 1 | 0 | 2 | 5 |
| `balanced --dict-profitable` vs `balanced` | `full` | `warm-cache` | 0.9906–1.0167 | 8 | 0 | 0 | 0 |
| `balanced --dict-profitable` vs `balanced` | `minimal` | `memfd` | 0.9088–1.4355 | 1 | 0 | 2 | 5 |
| `balanced --dict-profitable` vs `balanced` | `minimal` | `cold-cache` | 0.9704–1.2279 | 1 | 0 | 2 | 5 |
| `balanced --dict-profitable` vs `balanced` | `minimal` | `warm-cache` | 0.9891–1.0117 | 8 | 0 | 0 | 0 |

Omitting unprofitable dictionaries in `size --dict-profitable` eliminates Zstd
dictionary table initialization on `7/8` Go groups, producing `7/8` faster cold
`memfd` and `cold-cache` cells (`full` and `minimal`) against default `size`.
Conversely, the single slower cell against `best / no dict` (`govulncheck / stripped`)
and the two slower cells in `balanced --dict-profitable` vs `balanced`
(`actionlint / stripped` and `govulncheck / stripped`) correspond to the exact
groups where a net-positive dictionary was retained and parsed on cold decode.
All `warm-cache` rows (`8/8` across every stub and comparison) remain equivalent
within `[0.95, 1.05]` because verified cache hits bypass Zstd decoding.
