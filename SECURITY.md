# Security Policy

`microfat` takes the security, supply-chain integrity, and memory execution safety of multi-architecture fat binaries seriously.

---

## 1. Supported Versions

Only the latest release stream of `microfat` receives active security updates, bug fixes, and vulnerability patches.

| Version | Supported | Notes |
| :--- | :--- | :--- |
| `v0.1.x` | :white_check_mark: | Current active release stream |
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

- **Cryptographic Trailer Verification**: Fixed 56-byte cryptographic trailers (`\x00\xFA\x7FMICRO` magic) require valid SHA-256 payload checksums prior to decompression or execution.
- **Restricted Memory Execution**: In-memory ELF execution via `memfd_create` and fallback binaries in `$XDG_CACHE_HOME/microfat` are created with strictly private `0700` file modes restricted to the executing UID.
- **Resource Boundary Defense**: Automated cgroup v1 and v2 parsers enforce `GOMEMLIMIT` at a 90% container memory ceiling and bind `GOMAXPROCS` to CFS quotas to prevent noisy-neighbor Denial of Service (DoS).
