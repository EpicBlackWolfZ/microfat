# Release Artifact Selection Guide

This guide explains the artifact distribution model for `microfat` releases and provides guidance for operators, developers, and CI pipelines.

---

## 1. Distribution Model: Universal Fat Binaries Only

Starting in `v0.2.3`, `microfat` eliminates artifact fragmentation by distributing **only self-contained universal fat archives**. Individual per-microarchitecture CLI binaries (`_v1`, `_v2`, `_v3`, `_v4`, `_v8.0`, `_v8.2`, `_v9.0`) and standalone stub tarballs are no longer published as separate downloads.

### Available Release Archives

| Archive Name | Target Architecture | Included Binaries |
| :--- | :--- | :--- |
| `microfat_<version>_linux_amd64.tar.gz` | Linux x86-64 (`x86_64`) | `microfat`, `microfat-stub`, `microfat-stub-minimal` |
| `microfat_<version>_linux_arm64.tar.gz` | Linux AArch64 (`aarch64`) | `microfat`, `microfat-stub`, `microfat-stub-minimal` |

Each archive is accompanied by:
- A SHA-256 checksum in `checksums.txt` signed with Cosign (`checksums.txt.sig`).
- Software Bill of Materials (SBOM) in both **SPDX 2.3** (`.spdx.json`) and **CycloneDX** (`.cyclonedx.json`) formats.
- Verified hosted benchmark evidence (`benchmark-evidence-<tag>-<run>-<attempt>.tar.gz` and `.sha256`).

---

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
   - Includes full runtime features: CPU feature detection, Linux cgroup v1/v2 limit auto-tuning (`GOMEMLIMIT`, `GOMAXPROCS`), container OOM protection, and `memfd_create` in-memory execution with disk cache fallback.

3. **`microfat-stub-minimal` (Minimal Launcher Stub)**:
   - A lightweight stub compiled with `-tags=minimal`.
   - Strips non-essential reflection, diagnostics, and environment overrides to achieve the smallest possible ELF footprint.
   - Recommended for extreme cold-start sensitivity or deeply constrained serverless/embedded Linux containers.

---

## 3. Usage & Decision Guide

### Scenario A: Installing the `microfat` CLI
Download the single archive matching your OS and ISA (`linux_amd64` or `linux_arm64`):
```bash
curl -sSL https://github.com/EpicBlackWolfZ/microfat/releases/latest/download/microfat_0.2.3_linux_amd64.tar.gz | tar -xz -C /usr/local/bin
```
You do not need to choose a microarchitecture level; `microfat` detects and optimizes itself at launch.

### Scenario B: Packaging Your Application (`microfat pack`)
When `microfat` and `microfat-stub` reside in the same directory (or anywhere on `$PATH`), `microfat pack` **automatically discovers the launcher stub**:
```bash
# Explicit --stub is optional if microfat-stub is in the same directory or PATH
microfat pack -v v1=bin/app_v1 -v v3=bin/app_v3 -o dist/app
```

To use the minimal launcher stub, explicitly specify `--stub`:
```bash
microfat pack --stub /usr/local/bin/microfat-stub-minimal -v v1=bin/app_v1 -v v3=bin/app_v3 -o dist/app
```

### Scenario C: Deploying to Fleets and Containers
Do not distribute separate `v1` and `v3` container images or RPM/DEB packages. Produce one fat binary using `microfat pack` and distribute that single binary across your entire fleet. The binary will automatically run `v3` on modern nodes and `v1` on legacy nodes with zero manual configuration.

---

## 4. Format v1 Manifest Deprecation Notice

`microfat` Format v2 compact binary tables with mandatory frame digests are the production standard in `v0.2.2` and later.

### Deprecation Schedule

- **`v0.2.2`**: Format v2 became the default for all `microfat pack` and `build` commands. Format v1 reading remains supported for backwards compatibility.
- **`v0.2.3`**: `microfat inspect` and `microfat info` emit deprecation warnings when reading legacy Format v1 JSON manifests:
  ```text
  [microfat:warn] binary "app.fat" uses legacy Format v1 JSON manifest; Format v1 is deprecated and scheduled for removal in v0.4.0 (use Format v2 for production)
  ```
- **`v0.4.0`**: Legacy Format v1 reading support will be permanently removed.

### Migrating to Format v2
Re-package existing binaries using `microfat pack` (which uses Format v2 by default):
```bash
microfat pack --stub bin/microfat-stub -v v1=bin/v1 -v v3=bin/v3 -o dist/app
```
