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
Always download the versioned archive into an unprivileged temporary directory, verify the cryptographic signature and checksum, and install solely the three binaries:

```bash
set -euo pipefail

VERSION="0.2.3"
ARCH="amd64" # or "arm64"
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

ARCHIVE_NAME="microfat_${VERSION}_linux_${ARCH}.tar.gz"
RELEASE_URL="https://github.com/EpicBlackWolfZ/microfat/releases/download/v${VERSION}"

# 1. Download archive, checksums, and signature bundle using fail-on-error behavior
curl --fail -sSL -o "$WORK_DIR/$ARCHIVE_NAME" "$RELEASE_URL/$ARCHIVE_NAME"
curl --fail -sSL -o "$WORK_DIR/checksums.txt" "$RELEASE_URL/checksums.txt"
curl --fail -sSL -o "$WORK_DIR/checksums.txt.sig" "$RELEASE_URL/checksums.txt.sig"

# 2. Verify keyless Cosign signature against official release identity
cosign verify-blob \
  --bundle "$WORK_DIR/checksums.txt.sig" \
  --certificate-identity "https://github.com/EpicBlackWolfZ/microfat/.github/workflows/release.yml@refs/tags/v${VERSION}" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  "$WORK_DIR/checksums.txt"

# 3. Verify SHA-256 archive checksum (exact filename match only)
ENTRY=$(awk -v target="$ARCHIVE_NAME" '$2 == target || $2 == "*"target { print $1, $2 }' "$WORK_DIR/checksums.txt")
[ -n "$ENTRY" ] && [ "$(printf '%s\n' "$ENTRY" | wc -l)" -eq 1 ]
(cd "$WORK_DIR" && printf '%s\n' "$ENTRY" | sha256sum --check --status)

# 4. Extract and install solely the 3 executables to /usr/local/bin
tar -xzf "$WORK_DIR/$ARCHIVE_NAME" -C "$WORK_DIR"
sudo install -m 0755 "$WORK_DIR/microfat" /usr/local/bin/microfat
sudo install -m 0755 "$WORK_DIR/microfat-stub" /usr/local/bin/microfat-stub
sudo install -m 0755 "$WORK_DIR/microfat-stub-minimal" /usr/local/bin/microfat-stub-minimal
```
You do not need to choose a microarchitecture level; `microfat` detects and optimizes itself at launch.

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
> **Minimal Stub Selection**: `microfat-stub-minimal` is **never** silently selected by auto-discovery. To package with the minimal stub, you must pass `--stub /path/to/microfat-stub-minimal` explicitly.
> **Security Boundaries**: Implicit repository-relative paths (`bin/microfat-stub`, `../bin/microfat-stub`) are prohibited to prevent arbitrary stub injection.

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
