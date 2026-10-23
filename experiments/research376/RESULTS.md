# Local exploratory results

Source baseline: `ada376b4ffffaa3354becbfb70c1896934caf277`; Go 1.27.3;
Ryzen 3950X; real demo and microfat CLI amd64 v1/v2/v3 variants. Each cell uses
60 full-process observations. These are descriptive measurements from an
uncontrolled desktop, not qualified startup/p99 improvements.

| Application / codec | Complete artifact bytes | Forced memfd mean, ms | Verified cache mean, ms |
| --- | ---: | ---: | ---: |
| demo / none | 17,016,023 | 6.371 | 5.487 |
| demo / LZ4 | 11,599,811 | 8.739 | 5.527 |
| demo / Zstd | 9,626,974 | 11.809 | 5.552 |
| CLI / none | 38,556,887 | 12.634 | 10.235 |
| CLI / LZ4 | 23,451,118 | 17.141 | 10.189 |
| CLI / Zstd | 18,188,627 | 25.158 | 10.318 |

All 720 timed executions matched native v3 output and succeeded. The twelve
separate instrumented dispatch probes confirmed memfd/cache execution, v3
selection and GOMAXPROCS=4. Artifact indexes match the hash-pinned corpus.

The model makes deployment assumptions visible. For example, with one transfer
at 10 MB/s, packing charged once, and no warm launches, demo Zstd crosses the
uncompressed mean lifecycle cost near 118.47 launches. With a hypothetical 90%
warm fraction the same crossing is near 1070.94 launches. This is a sensitivity
example calculated from these observations, not an independently validated
deployment threshold. Prewarm setup/retention costs are omitted.

Verified cache launch means are relatively similar in this campaign because
the launch path reads and hashes the selected uncompressed payload regardless
of the stored codec. Forced memfd runs include extraction. The fixed codec
order and uncontrolled host prevent attributing observed differences solely to
the codec or using small differences to select defaults.

See [README.md](README.md) for cold/page-cache semantics, sampling limits and
replay commands. Inputs, every raw observation, artifact/dispatch identities,
collector source, corpus build recipe and checksums are retained in `evidence/`.
The analyzer's exact CDF arithmetic is tested independently, including atom and
adjacent-binary64 boundaries; measurement uncertainty remains separate.
