# Security Policy

`microfat` takes the security, supply-chain integrity, and memory execution safety of multi-architecture fat binaries seriously.

---

## 1. Supported Versions

The release streams marked supported below receive security updates and bug fixes. Use the latest patch in your stream.

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

Code-changing contributions and releases use the following automated checks. Documentation-only
changes follow the [CI classification policy](CONTRIBUTING.md); passing checks is evidence about
their tested scope, not proof that a program is free of vulnerabilities.

- **Static Application Security Testing (SAST)**: Automated **CodeQL Advanced** workflows continuously scan Go 1.27 abstract syntax trees (ASTs) and GitHub Actions configurations.
- **Secret Scanning & Push Protection**: Automated server-side push protection actively blocks commits containing API tokens, private keys, or credentials.
- **Supply-Chain Dependency Auditing**:
  - **`govulncheck`**: Scans the complete Go dependency graph against the official Go Vulnerability Database on every pull request.
  - **Dependabot**: Automated security updates for known vulnerable dependencies.
- **Git Secrets Detection**: **`gitleaks`** audits all repository commits and PR diffs in CI pipelines.
- **Release Immutability**: Published immutable releases lock their assets; an active tag ruleset separately protects release tags. All assets must be attached while the release is still a draft.

---

## 5. Runtime & Binary Security Architecture

`microfat` enforces strict runtime defense-in-depth:

- **Trailer & Payload Integrity Verification (SHA-256)**: Fixed 56-byte trailers (`\x00\xFA\x7FMICRO` magic) require a matching SHA-256 index before metadata use. Extracted payload bytes must match their SHA-256 digest before execution.
- **Mandatory Kernel Memory Sealing (`memfd_create`)**:
  - In-memory execution creates an anonymous RAM descriptor with close-on-exec and sealing support.
    Executable memfd flags and a bounded compatibility retry account for kernel policy; see
    [the implementation](cmd/microfat-stub/exec_linux.go) and
    [kernel-policy regression tests](cmd/microfat-stub/memfd_policy_linux_test.go).
  - Once variant payloads are extracted and validated against their embedded SHA-256 digests, the descriptor is sealed using `F_ADD_SEALS` with `F_SEAL_WRITE | F_SEAL_SHRINK | F_SEAL_GROW | F_SEAL_SEAL`.
  - `F_SEAL_WRITE` prevents any modification of the decompressed ELF binary in memory.
  - `F_SEAL_SHRINK` and `F_SEAL_GROW` prevent resizing or truncation of the executable memory region.
  - `F_SEAL_SEAL` permanently locks the seal set, forbidding any further seals or unsealing.
  - These seals protect the backing file contents; they do not provide general process-memory isolation or prevent a privileged debugger from modifying private mappings.
  - The launcher strictly treats unsealed descriptors as unsafe. If sealing is unsupported (`ENOSYS`, `EINVAL`) or blocked (`EPERM`), auto mode falls back cleanly to disk cache execution, while explicit memfd mode aborts immediately.
- **Descriptor-Bound Cache Fallback & TOCTOU Defense**:
  - New cache directories in `$XDG_CACHE_HOME/microfat` (or `/tmp/.microfat-<uid>`) are created with `0700` (`rwx------`). Existing directories must have the expected owner and no group/other write permission.
  - Cache entry opens use `O_RDONLY | O_CLOEXEC | O_NOFOLLOW | O_NONBLOCK` relative to the validated
    cache directory descriptor. `O_NOFOLLOW` rejects a symlink in the final entry component; it is
    not a general promise that every user-supplied path forbids ancestor symlinks.
  - Execution operates directly on the verified file descriptor via `/proc/self/fd/<fd>`, ensuring validation and execution bind to the exact same VFS inode and preventing pathname replacement from redirecting execution to a different inode.
- **Resource Boundary Defense**: Cgroup v1/v2 information informs extraction estimates and soft Go-runtime tuning. This does not guarantee freedom from OOM kills, CPU throttling or noisy-neighbor interference.

---

## 6. Payload Integrity vs Producer Authenticity (Hashing vs Signing)

> [!IMPORTANT]
> **Embedded SHA-256 digests provide payload integrity verification; they do not authenticate the producer of the fat binary.**

An important security distinction exists between cryptographic integrity hashing and digital signatures:

- **What Microfat Guarantees (Integrity & Corruption Detection)**:
  - **Bit-Flip & Network Corruption Detection**: Embedded SHA-256 digests detect truncation, transmission corruption, and storage degradation across all embedded payloads and metadata indices.
  - **Tampering Detection Against Partial Modification**: If an attacker or untrusted process modifies an embedded variant without altering the trailer or index table, `microfat` detects the SHA-256 mismatch and aborts execution immediately with `ErrPayloadCorrupted`.
  - **In-Memory Immutability**: Linux memory seals protect memfd backing contents. Descriptor-bound cache execution binds validation and execution to one inode; it does not make that inode immutable.
- **What Microfat Does NOT Guarantee (Authenticity & Origin Trust)**:
  - Microfat does **not** replace cryptographic digital signatures or public-key infrastructure (PKI).
  - An attacker with full write access to the fat binary file on disk could replace an embedded variant payload, recompute its SHA-256 digest, update the index manifest, and recalculate the 56-byte trailer checksum.
  - Hashing provides data integrity; it does not provide origin authenticity or proof that the binary was built by a trusted producer.
- **Production Best Practice**:
  - For production CI/CD pipelines, container base images, and public distribution, always pair `microfat` with supply-chain signing tools such as **Sigstore Cosign**, **GPG**, or system-level digital signatures to sign the final composite executable.

## 7. Launcher and cache deployment boundary

Full and minimal launchers reject real/effective UID or GID mismatches, nonzero `AT_SECURE`, and
executables carrying `security.capability`, before application file access, environment routing,
meta-commands or cache writes. Ordinary same-ID root execution is supported when these elevation
signals are absent. Missing/malformed secure-execution evidence fails closed. The probes assume the
Linux kernel and `/proc` are trustworthy; Go runtime startup precedes the application-level guard.
See Linux [auxiliary vector](https://man7.org/linux/man-pages/man3/getauxval.3.html) and
[capability](https://man7.org/linux/man-pages/man7/capabilities.7.html) semantics.

Cache entries must be regular files owned by the effective UID, without group/other write or special
permission bits. Size and SHA-256 are checked on the descriptor used for execution. Nonblocking opens
reject FIFO entries without waiting for a writer; special files and unsafe metadata are not repaired
by blindly replacing them.

Cache mode assumes **trusted same-UID writers** and local filesystem semantics for descriptors and
atomic rename. The owner or a process holding an existing writable descriptor can modify the same
inode after validation. Ownership/mode checks and an additional hash cannot establish immutability.
Deployments requiring immutable extracted payload storage must request `MICROFAT_EXEC_MODE=memfd`;
if creation or sealing is unavailable, that mode fails closed. Auto mode permits the weaker cache
boundary on fallback. A hostile same-UID isolation design requires separate review.

The kernel and privileged administrators are trusted. The ambient environment is configuration,
not authenticated provenance. Embedded hashes do not authenticate a hostile whole artifact: verify
publisher identity and exact artifact bytes externally **before first execution**.

## 8. Threat model and enforcement map

The protected assets are the selected executable bytes, predictable parsing/extraction, cache
isolation from other unprivileged identities, and the identity of published release downloads.
Neither the launcher nor `microfat verify` is a sandbox for an untrusted executable: the kernel
and Go runtime load the launcher before it can check its own embedded data. Authenticate the whole
archive before extracting/installing/running it, following the
[release verification instructions](docs/release-artifacts.md#scenario-a-installing-the-microfat-cli).

| Input or actor | Trust assumption and consequence |
| :--- | :--- |
| Source, compiler, dependencies and publisher | The selected publisher and its build process are trusted to produce suitable code. A checksum signature authenticates signed bytes, not source correctness, reproducibility or an assessed SLSA level. A compromised publisher can sign malicious code. |
| Download transport and storage | May corrupt or substitute archives, metadata or signatures. External verification must match the exact version, workflow identity and issuer, then the exact archive digest. An embedded hash can be recomputed by a whole-artifact attacker. |
| Packed metadata and payloads | Treat as bounded, untrusted parser input even after signature verification. Reject invalid ranges, sizes, codecs, tiers and hashes. Authentication does not eliminate malformed-input risks. |
| Same-UID processes and privileged administrators | Trusted. Same-UID cache writers, debuggers, existing writable descriptors and root are outside the isolation boundary. Pinning an inode stops pathname substitution, not writes to that inode. |
| Kernel, procfs and filesystem | Trusted Linux kernel/procfs identity, descriptor semantics, ownership checks and atomic rename are required. Hostile mounts, a malicious kernel and remote filesystems with different consistency semantics are outside the reviewed boundary. |
| Environment, arguments and manifests | Caller-controlled configuration, not provenance. Explicit paths select input; discovery avoids relative PATH directories. `MICROFAT_ORIGINAL_EXE` is only a location hint and requires running-payload consistency checks for stub discovery. It does not grant producer trust. |
| Payload application | Executes with the caller's ordinary authority and owns its behavior. Argument/environment fidelity and runtime tuning do not restrict filesystem, network, subprocess or syscall access. |

The following map links the implemented contract to regression coverage. Tests exercise the
listed boundaries; they are not a claim of protection against the out-of-scope actors above.

| Boundary | Implementation | Representative regression coverage |
| :--- | :--- | :--- |
| Refuse elevated launch before application side effects; permit same-ID root absent elevation signals | [privilege probes](cmd/microfat-stub/privilege_linux.go) | [probe ordering/failures](cmd/microfat-stub/privilege_linux_test.go), [real launch cases](tests/e2e/privilege_test.go) |
| Use the actual running launcher image across deployment unlink/replacement | [launcher entry](cmd/microfat-stub/main.go) | [paused-image replacement and metadata commands](tests/e2e/running_image_test.go) |
| Bounded trailer/index parsing and payload integrity | [format validation](internal/format/format.go), [bounded decoding](internal/codec/codec.go) | [malformed legacy JSON](tests/e2e/legacy_json_test.go), [corruption](tests/e2e/corruption_test.go), [format fuzzing](internal/format/format_fuzz_test.go) |
| Nonblocking regular-file inputs before parsing/building | [input descriptor validation](internal/inputfile/input.go) | [FIFO CLI rejection](tests/e2e/input_safety_test.go), [input unit tests](internal/inputfile/input_test.go) |
| Mandatory sealed memfd and explicit-mode failure | [execution](cmd/microfat-stub/exec_linux.go) | [sealing/fallback fault tests](cmd/microfat-stub/chaos_test.go), [executable memfd policy](cmd/microfat-stub/memfd_policy_linux_test.go) |
| Validated cache directory/entry descriptors; read-only verification | [cache descriptor operations](internal/format/cache_unix.go), [cache management](internal/cache/cache_unix.go) | [cache security](tests/e2e/cache_security_test.go), [FIFO entries](tests/e2e/cache_fifo_test.go), [read-only checks](tests/e2e/cache_readonly_test.go) |
| Consistency-checked executable location hints | [origin resolution](internal/builder/origin.go) | [origin regressions](internal/builder/origin_test.go), [stub discovery](internal/builder/stub_test.go) |
| Resource estimates with explicit unknown/unavailable observations | [cgroup observations](internal/cgroup/cgroup.go), [memory arithmetic](internal/cgroup/memory.go) | [unresolved/root cgroups](internal/cgroup/cgroup_test.go), [overflow and retained storage](internal/cgroup/memory_test.go) |

### Resource bounds and unavailable observations

These are current implementation limits, not a promise that allocating up to them succeeds on a
particular host. The linked constants and checks are authoritative when versions differ.

| Input | Current bound | Source |
| :--- | :--- | :--- |
| Serialized metadata index | 1,048,576 bytes (1 MiB) | `MaxIndexSize` in [format.go](internal/format/format.go) |
| One uncompressed variant | 1,073,741,824 bytes (1 GiB) | `MaxPayloadSize` in [format.go](internal/format/format.go); decoder output is also capped |
| Shared dictionary | 1,048,576 bytes (1 MiB) | `MaxDictionarySize` in [format.go](internal/format/format.go) |
| Legacy JSON nesting, including unknown fields | 128 levels | `MaxJSONDepth` in [format.go](internal/format/format.go) |
| Build manifest | 1,048,576 bytes (1 MiB) | `maxManifestBytes` in [manifest.go](internal/builder/manifest.go) |
| Secure-execution auxiliary vector read | 4,096 bytes | `maxAuxvBytes` in [privilege_linux.go](cmd/microfat-stub/privilege_linux.go) |

Range checks reject negative/overflowing or overlapping offsets and inconsistent declared sizes.
These per-input bounds do not cap total process RSS, the sum of variants, concurrent launchers or
all kernel accounting. Extraction estimates add output storage, dictionary, decoder and reserve;
sealed executable storage is deducted once from the raw memory ceiling before runtime headroom.
Cache page reclaimability and other processes' resource use can change available memory.

Missing, unreadable, malformed or unresolved cgroup observations do not establish unlimited
resources or available headroom. A known limit may inform a conservative estimate; an unavailable
one cannot establish OOM safety. `GOMEMLIMIT` is a Go soft limit, not a kernel reservation, and
`GOMAXPROCS` does not enforce CPU isolation. See [runtime tuning](docs/runtime-tuning.md),
[architecture](docs/architecture.md), and [lifecycle modes](docs/lifecycle-modes.md) for operational
effects and the limits of in-place transformations.
