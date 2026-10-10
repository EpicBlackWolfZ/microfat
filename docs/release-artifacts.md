# Release Artifact Selection Guide

This guide explains the artifact distribution model for `microfat` releases and provides guidance for operators, developers, and CI pipelines.

---

## 1. Distribution Model: Fat Product Archives and a Native Installer Helper

Starting in `v0.2.3`, the CLI and its companions are distributed in self-contained universal fat archives.
Individual per-microarchitecture CLI binaries and standalone stub tarballs are not separate downloads.
From `v0.3.0`, two additional baseline native helper assets bootstrap verified installation without Go.
They are not installed beside the three public products.

### Available Release Archives

| Archive Name | Target Architecture | Included Binaries |
| :--- | :--- | :--- |
| `microfat_<version>_linux_amd64.tar.gz` | Linux x86-64 (`x86_64`) | `microfat`, `microfat-stub`, `microfat-stub-minimal` |
| `microfat_<version>_linux_arm64.tar.gz` | Linux AArch64 (`aarch64`) | `microfat`, `microfat-stub`, `microfat-stub-minimal` |
| `microfat-install_<version>_linux_amd64` | Linux x86-64 baseline v1 | Static native installation helper (from v0.3.0) |
| `microfat-install_<version>_linux_arm64` | Linux AArch64 baseline v8.0 | Static native installation helper (from v0.3.0) |

Each product archive and native helper is accompanied by:

- A SHA-256 checksum in `checksums.txt` signed with Cosign (`checksums.txt.sig`).
- From **v0.2.5**, Software Bill of Materials (SBOM) in **SPDX 3.0.1 JSON-LD** (`.spdx.json`)
  and **CycloneDX 1.7 JSON** (`.cyclonedx.json`). Published v0.2.3/v0.2.4 documents retain
  their historical SPDX 2.3 and CycloneDX 1.5 formats.
- Verified hosted benchmark evidence (`benchmark-evidence-<tag>-<run>-<attempt>.tar.gz` and `.sha256`).

### SBOM inventory and verification

The Go generator reads the archive, all three executable files and every embedded variant.
It records their SHA-256 hashes, architecture, variant tier, build settings, source revision
when present, and each binary's linked Go module versions, replacements and module sums.
Different dependency versions in different variants remain separate components.

Native helper SBOMs use the explicit `native-installer` artifact kind and contain one native
executable with its actual linked Go dependencies. They declare the project Apache-2.0 license
without pretending license text was extracted from a raw ELF. Baseline ISA, Linux architecture,
CGO-disabled build settings, expected main package and absence of an ELF interpreter are checked.
The historical inventory remains six signed payloads; v0.3.0 requires twelve (four artifacts and
their eight SBOMs), without accepting arbitrary extra checksum entries.

Release contract validation and extraction consume gzip through EOF to verify every member's
CRC and uncompressed size, including data after the tar end marker. They limit the complete
decompressed stream to 512 MiB. Full archive extraction retains the 250 MiB per-file and 500 MiB
extracted-file limits; single-file extraction retains its 250 MiB target-file limit. After tar ends,
at most 1 MiB of zero padding is allowed, including padding in additional
gzip members. Empty additional members are accepted; nonzero trailing data, a second tar archive,
raw trailing bytes and malformed or truncated gzip members are rejected. The archive SHA-256
covers the complete compressed artifact, including accepted padding members. These format checks
complement the independent checksum and signature authentication described in the
[verification contract](release-verification.md).

CycloneDX expresses archive/executable/variant containment through nested components and
linked modules through dependency edges. The pinned cdxgen **cdx-convert v13.1.0** tool receives
a flattened conversion view; Go verifies that conversion preserves all components, hashes,
package identifiers, metadata and dependencies, then supplies explicit SPDX containment,
creator-tool and declared-license relationships. The standalone converter requires neither
Python, BLINT nor a separately installed Node runtime.

Project license text comes from the archived `LICENSE` file. The known Apache-2.0 text is
identified by its verified content hash. Go build information does not supply dependency
licenses; those components explicitly record that the license is unavailable from this source.
Local module replacements without a registry version do not receive an invented package URL.

Both documents must pass pinned offline official schemas and independent checks against the
archive's actual bytes and Go build information before they enter the signed checksum inventory.
The CycloneDX schema package is **1.7.2**, while documents use `specVersion: "1.7"`.
SPDX documents use the official **3.0.1 JSON-LD context and graph**, rather than the historical
`spdxVersion`/`packages` layout. Asset filename suffixes remain unchanged, so consumers should
inspect document markers when selecting a parser. Historical readers remain available, while
new releases and v0.2.5 snapshots reject the historical schemas.

---

Maintainers can dispatch [Published Release Audit](https://github.com/EpicBlackWolfZ/microfat/actions/workflows/release-audit.yml) with a published immutable tag. It verifies the checksum signature against the exact tag workflow and source commit before executing downloaded products on native amd64 and ARM64 runners. The audit covers both formats, full/minimal launchers, codecs/dictionaries, automatic/memfd/cache dispatch, metadata operations and corrupt-payload rejection. Its retained evidence identifies the downloaded bytes; benchmark publication qualification remains a separate gate.

## 2. Archive Contents & Binary Roles

Extracting `microfat_<version>_linux_<arch>.tar.gz` provides:

```text
├── microfat                   # Universal fat CLI (auto-dispatches optimal variant on host)
├── microfat-stub              # Standard launcher stub binary for packaging
├── microfat-stub-minimal      # Minimal launcher stub binary (reduced footprint, reflection-free)
├── README.md                  # Project overview and quick start
├── LICENSE                    # Apache 2.0 license
├── SECURITY.md                # Security policy and threat boundary
└── docs/                      # Full architecture, optimization, and reference manuals
```

### Binary Roles

1. **`microfat` (The CLI)**:
   - The universal developer and CI command-line tool.
   - Self-dispatches the highest supported microarchitecture variant for your machine (e.g. `v4` on AVX-512 systems, `v3` on AVX2 systems, falling back to `v1`).
   - Used for `microfat detect`, `microfat pack`, `microfat inspect`, `microfat verify`, and `microfat trim`.

2. **`microfat-stub` (Standard Launcher Stub)**:
   - The default launcher stitched to the head of fat executables created by `microfat pack`.
   - Includes full runtime features: CPU feature detection, Linux cgroup v1/v2 soft-runtime auto-tuning (`GOMEMLIMIT`, `GOMAXPROCS`), and cache-first auto dispatch. It verifies an existing cache descriptor, uses sealed memfd on a miss, and can materialize verified cache after a cold memfd failure. See the [dispatch contract](architecture.md#7-cache-first-auto-dispatch--descriptor-bound-execution).

3. **`microfat-stub-minimal` (Minimal Launcher Stub)**:
   - A lightweight stub compiled with `-tags=minimal`.
   - Strips non-essential reflection, diagnostics, and environment overrides to achieve the smallest possible ELF footprint.
   - Recommended for extreme cold-start sensitivity or deeply constrained serverless/embedded Linux containers.

---

## 3. Usage & Decision Guide

### Scenario A: Installing the `microfat` CLI

The [official Homebrew tap](https://github.com/EpicBlackWolfZ/homebrew-tap) selects these same archives
for Linux amd64 and arm64. Its first cask requires the public v0.3.0 release and a merged recipe PR;
see [Homebrew commands](installation.md#homebrew-linux-amd64-and-arm64). The tap generator consumes
the authenticated release inventory directly, so GoReleaser's `meta: true` archive output does not
need fabricated platform metadata or a second packaging pass.

Use the [verified installer](installation.md) to authenticate the release and publish all three products
as one owned generation. The first bootstrap needs published v0.3.0 helper assets; the guide also covers
a trusted source helper and independently provisioned verifier. Historical v0.2.3 through v0.2.5 remain
installable without changing their immutable archives.

The [verification contract](release-verification.md) defines the exact tag identity, independent verifier
pins and authentication before extraction. No downloaded product authenticates itself. User paths are
the default; system mode requires explicit roots and privileges. Existing manual/package-manager files
are never silently overwritten.

### Scenario B: Packaging Your Application (`microfat pack`)
When `microfat` and `microfat-stub` reside in the same directory (or on `$PATH`), `microfat pack` automatically discovers the launcher stub:
```bash
# Explicit --stub is optional when microfat-stub is installed beside microfat or in PATH
microfat pack -v v1=bin/app_v1 -v v3=bin/app_v3 -o dist/app
```

#### Launcher Stub Resolution Precedence & Security Policy
`microfat pack` resolves the launcher stub using a strict 5-tier evaluation order:
1. **Explicit `--stub <path>` CLI flag**: Takes absolute precedence. Must resolve to an existing regular file; fails fast with no fallback if missing or invalid.
2. **Manifest `stub:` field**: Resolved relative to the manifest directory. Fails fast if missing.
3. **Sibling Installed Stub**: Located beside the running `microfat` CLI executable. When running under `memfd` or disk cache dispatch, origin resolution validates the active process image against `/proc/self/exe` digest metadata before trusting the original binary's directory.
4. **Absolute directories in `$PATH`**: System `$PATH` is scanned in order, inspecting only absolute directory entries. Empty entries, current working directory (`.`), and relative paths are rejected for security.
5. **Explanatory Error**: Halts execution with clear instructions listing all resolution tiers.

> [!IMPORTANT]
> **Minimal Stub Selection**: `microfat-stub-minimal` is never silently selected by default full-profile discovery. To package with the minimal stub, specify `--stub-profile minimal` (or `stub_profile: minimal` in the manifest), or pass `--stub /path/to/microfat-stub-minimal` explicitly.
> **Security Boundaries**: Implicit repository-relative paths (`bin/microfat-stub`, `../bin/microfat-stub`) are prohibited to prevent arbitrary stub injection.

### Preserve packed bytes during packaging and installation

Strip/debug-split the native payloads and launcher **before** packing. The final packed executable stores payloads, index and trailer after the stub ELF; ELF rewriting tools can discard those bytes. Do not run `strip`, `objcopy`, automatic debug splitting, or `install -s` on it. This applies to the published fat `microfat` CLI as well as your own packed applications, for both formats and launcher profiles.

```bash
# Native inputs only; collect separate debug information here if needed.
strip bin/app_v1 bin/app_v3 bin/microfat-stub
microfat pack --stub bin/microfat-stub -v v1=bin/app_v1 -v v3=bin/app_v3 -o dist/app
microfat verify dist/app
install -m 0755 dist/app /desired/prefix/bin/app
```

In a Debian `debian/rules` recipe, exclude the final packed file from `dh_strip` (the recipe line needs a tab):

```make
override_dh_strip:
	dh_strip --exclude=usr/bin/app
```

Alternatively, `DEB_BUILD_OPTIONS=nostrip` disables stripping for the build. `noautodbgsym` alone is insufficient because debuglink rewriting can still happen; see [dh_strip's documented controls](https://manpages.debian.org/unstable/debhelper/dh_strip.1.en.html). Also exclude the packed file from any separate `dh_dwz`, `objcopy`, or custom ELF rewriting steps. For RPM and other packaging pipelines, configure their strip/debug extraction hooks to skip the final packed file, or pack after those hooks finish. Verify the actual extracted package binary with `microfat verify` and a smoke run, and compare its hash with the packed input before signing/publishing the package.

GNU strip removal is covered by a bounded regression test; this is not a claim of compatibility with every GNU/LLVM transformation. A damaged artifact must be obtained again or rebuilt; see [trailer failure diagnostics](troubleshooting.md#7-tampering--payload-integrity-verification-failures).

### Scenario C: Deploying to Fleets and Containers
Do not distribute separate `v1` and `v3` container images or RPM/DEB packages. Produce one fat binary using `microfat pack` and distribute that single binary across your entire fleet. The binary will automatically run `v3` on modern nodes and `v1` on legacy nodes with zero manual configuration.

---

## 4. Format v1 Manifest Deprecation Notice

`microfat` Format v2 compact binary tables with mandatory frame digests are the production standard in `v0.2.2` and later.

### Deprecation Schedule

- **`v0.2.2`**: Format v2 became the default for all `microfat pack` and `build` commands. Format v1 reading remains supported for backwards compatibility.
- **`v0.2.3`**: `microfat inspect` and `microfat info` emit deprecation warnings to `stderr` when reading legacy Format v1 JSON manifests:
  ```text
  [microfat:warn] binary "app.fat" uses legacy Format v1 JSON manifest; Format v1 is deprecated and scheduled for removal in v0.4.0 (use Format v2 for production)
  ```
  Pure JSON output on `stdout` via `--json` remains parseable and uncontaminated.
- **`v0.4.0`**: Legacy Format v1 reading support will be permanently removed.

### Migrating to Format v2
Re-package existing binaries using `microfat pack` (which uses Format v2 by default):
```bash
microfat pack -v v1=bin/v1 -v v3=bin/v3 -o dist/app
```
