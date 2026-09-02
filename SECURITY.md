# Security Policy

`microfat` takes the security, supply-chain integrity, and memory execution safety of multi-architecture fat binaries seriously.

---

## 1. Supported Versions

Only the latest release stream of `microfat` receives active security updates, bug fixes, and vulnerability patches.

| Version | Supported | Notes |
| :--- | :--- | :--- |
| `v0.2.x` | :white_check_mark: | Current active release stream |
| `v0.1.x` | :white_check_mark: | Supported maintenance release stream |
| `< v0.1.0` | :x: | End of life |

---

## 2. Reporting a Vulnerability

If you discover a potential security vulnerability, memory safety flaw, or privilege escalation issue in `microfat`, please **DO NOT** report it via public GitHub Issues or Discussions.

### Responsible Disclosure Process:

1. **GitHub Private Vulnerability Reporting (Preferred)**:
   - Submit a private report via [GitHub Security Advisories](https://github.com/EpicBlackWolfZ/microfat/security/advisories/new).
   - Provide reproduction steps, potential impact, affected CPU architectures/cgroup configurations, and suggested fixes (if available).

2. **Direct Maintainer Contact**:
   - If GitHub Advisories is inaccessible, contact the project maintainer directly via GitHub profile: [@EpicBlackWolfZ](https://github.com/EpicBlackWolfZ).

### What to Include in Your Report:
- **Vulnerability Description**: Clear explanation of the attack vector (e.g., ELF parser corruption, payload trailer spoofing, `memfd_create` FD leak).
- **Proof-of-Concept (PoC)**: Minimal reproduction code, test harness, or sample fat binary.
- **Environment Details**: Linux kernel version, host CPU architecture (AMD64 / ARM64), and cgroup version (v1 or v2).

---

## 3. Vulnerability Response Timeline

- **Initial Response & Acknowledgement**: Within **48 hours** of receipt.
- **Triage & PoC Reproduction**: Within **5 business days**.
- **Patch & Coordinated Advisory**: Coordinated disclosure and patch release within **30 days** of confirmed vulnerability.

---

## 4. Continuous Security & Supply-Chain Guarantees

Every contribution and release in `microfat` undergoes automated multi-layer security auditing:

- **Static Application Security Testing (SAST)**: Automated **CodeQL Advanced** workflows continuously scan Go 1.27 abstract syntax trees (ASTs) and GitHub Actions configurations.
- **Secret Scanning & Push Protection**: Automated server-side push protection actively blocks commits containing API tokens, private keys, or credentials.
- **Supply-Chain Dependency Auditing**:
  - **`govulncheck`**: Scans the complete Go dependency graph against the official Go Vulnerability Database on every pull request.
  - **Dependabot**: Automated security updates with malware detection and grouped security pull requests.
- **Git Secrets Detection**: **`gitleaks`** audits all repository commits and PR diffs in CI pipelines.
- **Release Immutability**: All published release assets and `v*` release tags are permanently protected and immutable via Repository Rulesets.

---

## 5. Runtime & Binary Security Architecture

`microfat` enforces strict runtime defense-in-depth:

- **Trailer & Payload Integrity Verification (SHA-256)**: Fixed 56-byte trailers (`\x00\xFA\x7FMICRO` magic) require matching SHA-256 index and payload checksums prior to decompression or execution.
- **Mandatory Kernel Memory Sealing (`memfd_create`)**:
  - In-memory execution creates an anonymous RAM descriptor via `memfd_create("microfat_payload", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)`.
  - Once variant payloads are extracted and validated against their embedded SHA-256 digests, the descriptor is sealed using `F_ADD_SEALS` with `F_SEAL_WRITE | F_SEAL_SHRINK | F_SEAL_GROW | F_SEAL_SEAL`.
  - `F_SEAL_WRITE` prevents any modification of the decompressed ELF binary in memory.
  - `F_SEAL_SHRINK` and `F_SEAL_GROW` prevent resizing or truncation of the executable memory region.
  - `F_SEAL_SEAL` permanently locks the seal set, forbidding any further seals or unsealing.
  - This mitigates local code injection, `/proc/self/mem` write races, and tampering prior to executing the process image via `/proc/self/fd/<fd>`.
  - The launcher strictly treats unsealed descriptors as unsafe. If sealing is unsupported (`ENOSYS`, `EINVAL`) or blocked (`EPERM`), auto mode falls back cleanly to disk cache execution, while explicit memfd mode aborts immediately.
- **Descriptor-Bound Cache Fallback & TOCTOU Defense**:
  - Fallback binaries in `$XDG_CACHE_HOME/microfat` (or `/tmp/.microfat-<uid>`) are isolated with strict `0700` (`rwx------`) permissions per-user.
  - Binaries are opened exclusively using `unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW` to guarantee refusal of symlink traversal with `ELOOP`.
  - Execution operates directly on the verified file descriptor via `/proc/self/fd/<fd>`, ensuring validation and execution bind to the exact same VFS inode and completely eliminating Time-of-Check to Time-of-Use (TOCTOU) file replacement races.
- **Resource Boundary Defense**: Automated cgroup v1 and v2 parsers enforce `GOMEMLIMIT` at a 90% container memory ceiling and bind `GOMAXPROCS` to CFS quotas to prevent noisy-neighbor Denial of Service (DoS).

---

## 6. Payload Integrity vs Producer Authenticity (Hashing vs Signing)

> [!IMPORTANT]
> **Embedded SHA-256 digests provide payload integrity verification; they do not authenticate the producer of the fat binary.**

An important security distinction exists between cryptographic integrity hashing and digital signatures:

- **What Microfat Guarantees (Integrity & Corruption Detection)**:
  - **Bit-Flip & Network Corruption Detection**: Embedded SHA-256 digests detect truncation, transmission corruption, and storage degradation across all embedded payloads and metadata indices.
  - **Tampering Detection Against Partial Modification**: If an attacker or untrusted process modifies an embedded variant without altering the trailer or index table, `microfat` detects the SHA-256 mismatch and aborts execution immediately with `ErrPayloadCorrupted`.
  - **In-Memory Immutability**: Linux kernel memory seals (`F_SEAL_WRITE | F_SEAL_SHRINK | F_SEAL_GROW | F_SEAL_SEAL`) and descriptor-bound `O_NOFOLLOW` execution prevent post-decompression tampering and TOCTOU races.
- **What Microfat Does NOT Guarantee (Authenticity & Origin Trust)**:
  - Microfat does **not** replace cryptographic digital signatures or public-key infrastructure (PKI).
  - An attacker with full write access to the fat binary file on disk could replace an embedded variant payload, recompute its SHA-256 digest, update the index manifest, and recalculate the 56-byte trailer checksum.
  - Hashing provides data integrity; it does not provide origin authenticity or proof that the binary was built by a trusted producer.
- **Production Best Practice**:
  - For production CI/CD pipelines, container base images, and public distribution, always pair `microfat` with supply-chain signing tools such as **Sigstore Cosign**, **GPG**, or system-level digital signatures to sign the final composite executable.
