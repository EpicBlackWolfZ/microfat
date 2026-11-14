# Retained historical service-p99 investigation

These archives retain every cohort of [run 40139089920, attempt
1](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40139089920), including
pretrial failures. See the [results report](../../../docs/hosted-p99-investigation-results.md)
and [prospective protocol](../../../docs/hosted-p99-investigation.md).

Each archive contains the original artifact's extracted files under `raw/`,
GitHub artifact/job metadata and job log, an audit, a manifest, the tracked
measurement source at `cedeb50c18901b1c7fcdf8550cb04126c5821def`, and an internal
`SHA256SUMS`. Its sidecar verifies the exact retained tarball. The manifest
records the original ZIP's separate GitHub SHA-256 digest; repackaging changes
the container bytes, so the ZIP and tarball digests intentionally differ.
The outer archives use sorted paths and normalized timestamps; files under
`raw/` retain their original bytes. GitHub's artifact expiry does not remove
these copies.

From the repository root, verify the sidecars and extract a cohort outside the
checkout:

```bash
sha256sum -c benchmarks/testdata/p99-230/*.sha256
evidence_dir="$(mktemp -d)"
tar -xzf benchmarks/testdata/p99-230/hosted-40139089920-1-memory32-0.tar.gz -C "${evidence_dir}"
cd "${evidence_dir}/memory32-cohort-0"
sha256sum -c SHA256SUMS
```

Recompute the frozen Go qualification and analysis on a copy, leaving the
checksummed evidence untouched. This needs Go 1.27.3 and the dependencies in
the retained `go.mod`/`go.sum` (download access if not already cached):

```bash
cp -a raw/p99-investigation "${evidence_dir}/replay"
cd measurement-source
GOTOOLCHAIN=go1.27.3 go run ./internal/cmd/p99-investigation \
  summarize --output "${evidence_dir}/replay"
jq 'del(.bundle)' ../raw/p99-investigation/summary.json > "${evidence_dir}/original.json"
jq 'del(.bundle)' "${evidence_dir}/replay/summary.json" > "${evidence_dir}/replayed.json"
diff -u "${evidence_dir}/original.json" "${evidence_dir}/replayed.json"
diff -u ../raw/p99-investigation/summary.md "${evidence_dir}/replay/summary.md"
```

Only the absolute `.bundle` location changes during successful offline replay.
Incomplete mixed/v4 cohorts deliberately return a nonzero status after writing
their summaries; their empty-bundle summaries replay byte-for-byte. Preserve
that failure status. To replay another cohort, substitute its archive and
directory names. The raw historical source worktrees include original runner
`.git` pointers and are provenance snapshots, not portable Git checkouts; the
separate `measurement-source/` tree supports the analysis command above.

The stored source and telemetry are evidence fixtures. They are outside the
GoReleaser binary-archive file lists and are not embedded into the executables.
