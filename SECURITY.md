# Security Policy

`microfat` takes the security and integrity of fat binary packaging and memory execution seriously.

---

## 1. Supported Versions

Only the latest release of `microfat` receives active security updates and vulnerability patches.

| Version | Supported          |
| :--- | :--- |
| `v0.1.x` | :white_check_mark: |
| `< v0.1.0` | :x:                |

---

## 2. Reporting a Vulnerability

If you discover a security vulnerability or potential privilege escalation issue within `microfat`, please **DO NOT** disclose it publicly via GitHub Issues or discussions.

### Reporting Process:

1. **Private GitHub Security Advisory**:
   - Navigate to [GitHub Security Advisories](https://github.com/EpicBlackWolfZ/microfat/security/advisories/new) on the repository.
   - Fill out the advisory form with detailed reproduction steps, potential impact, affected architectures, and proposed patches (if available).

2. **Direct Contact**:
   - If GitHub Advisories is unavailable, contact the project maintainer directly via GitHub profile: [@EpicBlackWolfZ](https://github.com/EpicBlackWolfZ).

### What to Include:
- Description of the vulnerability and attack vector (e.g., ELF parsing buffer overrun, `memfd_create` leak, trailer spoofing).
- Minimal reproducible proof-of-concept (PoC) code or sample binary payload.
- System environment (Linux kernel version, host CPU microarchitecture, cgroup version).

---

## 3. Vulnerability Response Timeline

- **Initial Response**: Within 48 hours of receipt.
- **Triage & Reproduction**: Within 5 business days.
- **Patch & Advisory Release**: Coordinated disclosure within 30 days of confirmed vulnerability.

---

## 4. Security Design Principles in microfat

- **Integrity Validation**: All embedded ELF variants are validated with SHA-256 integrity checksums verified against cryptographic trailers prior to memory execution.
- **Restricted Permissions**: In-memory payloads (`memfd_create`) and cache fallback binaries are marked strictly private (`0700` mode) and owned by the executing UID.
- **Container Isolation**: Automatic cgroup memory (`GOMEMLIMIT` at 90% ceiling) and CPU CFS quota detection prevent container resource starvation and denial-of-service.
