# Troubleshooting, Diagnostics & Operational Runbook

[**← Advanced Optimizations**](advanced-optimizations.md) | [**Main Index**](../README.md#documentation-guide) | [**Demo & Benchmarks →**](../examples/demo/README.md)

---

This runbook guides engineers through diagnosing runtime issues, addressing hardened container restrictions (seccomp, read-only rootfs), resolving permission issues, managing AVX-512 downclocking policies, and tuning container GC dynamics.

---

## 1. Fast Diagnostic Workflow with `microfat doctor`

Before debugging application code, run `microfat doctor` to check the execution environment:

```bash
# 1. Run full environment audit
microfat doctor

# 2. Strict mode (useful in CI/CD pipeline smoke tests)
microfat doctor --strict

# 3. Machine-readable JSON report for automated monitoring
microfat doctor --json
```

### Interpreting Doctor Report Status Glyphs

| Glyph | Meaning | Action Required |
| :--- | :--- | :--- |
| `[✔]` | **Pass**: Capability is fully available and optimal. | None. |
| `[!]` | **Warning**: Functional, but running with sub-optimal configuration (e.g. AVX-512 downclock risk or disk cache fallback). | Review recommended settings below. |
| `[✖]` | **Failure**: Execution is blocked (e.g. unwritable cache AND blocked `memfd_create`). | Follow remediation steps below. |

---

## 2. In-Memory Execution Issues (`memfd_create`)

### Symptom: `memfd_create failed: operation not permitted` or `permission denied`

#### Root Cause:
Linux `memfd_create` (syscall 319 on x86_64, syscall 279 on arm64) is restricted by a custom Docker/Kubernetes seccomp profile, or older Linux kernels (< 3.17).

#### Remediation 1: Update Seccomp Profile (Recommended)
Allow `memfd_create` in your Kubernetes or Docker seccomp security profile:

```json
{
  "defaultAction": "SCMP_ACT_ERRNO",
  "architectures": ["SCMP_ARCH_X86_64", "SCMP_ARCH_AARCH64"],
  "syscalls": [
    {
      "names": ["memfd_create"],
      "action": "SCMP_ACT_ALLOW"
    }
  ]
}
```

In Kubernetes Pod Security Context:
```yaml
securityContext:
  seccompProfile:
    type: RuntimeDefault  # RuntimeDefault permits memfd_create on modern runtimes
```

#### Remediation 2: Enable Prewarmed Disk Cache
If seccomp cannot be modified, configure Microfat to execute from disk cache:

```yaml
env:
  - name: MICROFAT_EXEC_MODE
    value: "cache"
  - name: MICROFAT_CACHE_DIR
    value: "/tmp/.cache/microfat"
```

---

## 3. Read-Only Container Root Filesystems (`readOnlyRootFilesystem: true`)

### Symptom: `mkdir /home/deployer/.cache: read-only file system`

#### Root Cause:
When running in hardened containers (`readOnlyRootFilesystem: true`) or distroless images without a home directory:
- Microfat prefers `memfd_create` (which requires **zero disk writes**).
- However, if `memfd_create` is also blocked, the fallback to `$HOME/.cache/microfat` fails because the rootfs is read-only.

#### Remediation: Mount a Writable `tmpfs`
Mount an in-memory `emptyDir` (or `tmpfs`) volume at `/tmp` and point `MICROFAT_CACHE_DIR` to it:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: hardened-microfat-service
spec:
  containers:
    - name: app
      image: myapp:latest
      securityContext:
        readOnlyRootFilesystem: true
        allowPrivilegeEscalation: false
      env:
        - name: MICROFAT_CACHE_DIR
          value: "/tmp/microfat-cache"
      volumeMounts:
        - name: tmp-volume
          mountPath: /tmp
  volumes:
    - name: tmp-volume
      emptyDir:
        medium: Memory
        sizeLimit: 128Mi
```

---

## 4. Disk Cache Permissions & Multi-Tenant Security

### Symptom: `cache directory permissions are insecure` or `permission denied`

#### Security Model Invariants:
1. All Microfat cache directories (`$XDG_CACHE_HOME/microfat` or `/tmp/.microfat-<uid>`) are strictly created with `0o700` (`rwx------`) permissions.
2. Extracted variant binaries are owned by the current UID and locked to `0o700`.
3. Multi-tenant hosts automatically isolate cache entries per UID into `/tmp/.microfat-<uid>`.

#### Resolving Permission Conflicts:
If a previous execution as `root` created the cache directory with `0700`, subsequent runs as a non-root user (`UID 10001`) will fail to write to the directory.

```bash
# Clean up or fix ownership of the cache path
sudo rm -rf /tmp/.microfat-* ~/.cache/microfat

# Or set an explicit user-private cache directory
export MICROFAT_CACHE_DIR="/tmp/microfat-$(id -u)"
```

---

## 5. AVX-512 Frequency Downclocking on Intel CPUs

### Symptom: `microfat doctor` emits `[!] Skylake-X / Cascade Lake detected`

#### Root Cause:
On Intel Xeon Family 6 Model 85 processors (Skylake-X, Cascade Lake), 512-bit vector instructions trigger CPU core power throttling (`License 2`), reducing clock frequencies by **15% to 25%** across all scalar threads on that core for ~2ms.

#### Modern Architecture Status:
- **AMD Zen 4 & Zen 5**: Native dual 256-bit or full 512-bit vector units with **zero downclock penalty**.
- **Intel Sapphire Rapids / Emerald Rapids**: Frequency penalties are negligible.

#### Remediation: Apply Safe AVX-512 Policy
Enable automatic downclock mitigation via environment variable:

```bash
# Enable automatic Skylake-X downclock protection (caps selection at v3 on affected CPUs)
export MICROFAT_POLICY=safe_avx512

# Or use the boolean flag
export MICROFAT_AVX512_DOWNCLOCK_PROTECTION=1
```

When building container images for heterogeneous clusters:
```bash
# Trim fat binary to maximum level v3 for safe deployment
microfat trim app.fat --max-level v3 --policy safe_avx512 -o app_safe.fat
```

---

## 6. Container Garbage Collection & CFS CPU Quota Throttling

### Symptom: High p99 Latency Spikes / High GC CPU Utilization (> 25%)

#### Root Cause (The GC Thrashing Cliff):
If steady-state live heap memory exceeds $\frac{\text{GOMEMLIMIT}}{1 + \text{GOGC}/100}$, Go enters continuous GC cycles, saturating the 33% GC CPU limiter.

#### Diagnostic Log Inspection:
Set `GODEBUG=gctrace=1` to observe runtime memory pacing:

```text
gc 14 @1.245s 3%: 0.12+1.4+0.04 ms clock, 0.48+2.8/1.2/0+0.16 ms cpu, 450->452->220 MB, 870 MB goal, 0 MB stacks, 0 MB globals, 4 procs (forced)
```

| Field | Analysis | Health Status |
| :--- | :--- | :--- |
| `3%` | Process CPU time spent in GC | Healthy ($\le 10\%$). If $> 25\%$, application is nearing GC thrashing. |
| `450->452->220 MB` | Live heap set ($220\text{ MB}$) vs peak ($452\text{ MB}$) | Healthy. Live set should be $\le 50\%$ of target goal. |
| `870 MB goal` | Target heap ceiling configured by `GOMEMLIMIT` | Matches Microfat's calculated limit. |

#### Workload Profile Tuning Recipes:

```bash
# For Latency-Critical API Services (eliminates p99 latency cliffs):
export MICROFAT_GC_PROFILE=latency_critical   # Sets GOGC=75

# For Micro-Containers (< 512MB RAM):
export MICROFAT_GC_PROFILE=memory_constrained # Sets GOGC=40, MICROFAT_MEM_RATIO=0.80

# For High-Throughput Batch/ETL Streams:
export MICROFAT_GC_PROFILE=batch_etl          # Sets GOGC=off (triggers only at 90% ceiling)

# For Dynamic Formula Sizing:
export MICROFAT_GC_PROFILE=adaptive
export MICROFAT_LIVE_HEAP_ESTIMATE=150MB
```

---

## 7. Tampering & Cryptographic Verification Failures

### Common Verification Error Sentinels:

| Error | Root Cause | Remediation |
| :--- | :--- | :--- |
| `invalid microfat magic bytes at EOF` | File is a standard ELF or truncated binary without the fixed 56-byte trailer (`\x00\xFA\x7FMICRO`). | Ensure file was packed with `microfat pack`. |
| `index SHA-256 checksum mismatch` | The binary index manifest was modified or corrupted in transit. | Re-download or rebuild fat executable. Run `microfat verify <bin>`. |
| `shared dictionary SHA-256 checksum mismatch` | Embedded Zstandard dictionary was corrupted or truncated. | Verify dictionary size and SHA-256 hash with `microfat inspect <bin>`. |
| `variant payload extends beyond binary boundary` | Binary was truncated during copy or download. | Check complete file size against `stat` and rebuild. |

---

## 8. Frequently Asked Questions (FAQ)

### Q1: Does Microfat add latency to long-running microservices?
**No.** The ~1.5ms decompression overhead occurs **only once at process launch** when streaming into anonymous RAM (`memfd_create`). Once running, execution is 100% native machine code with zero wrapper daemon overhead.

### Q2: Why does `runtime.NumCPU()` still return the host core count?
In Go, `runtime.NumCPU()` queries the host hardware. However, Microfat automatically sets `GOMAXPROCS` to match the container's CFS CPU quota (e.g. `2` cores for `cpu: 2000m`), preventing scheduler oversubscription and thread throttling.

### Q3: How do I eliminate startup overhead entirely for CLI tools?
Use Raw Native ELF mode:
```bash
./my-cli --microfat:optimize
```
This permanently replaces the file on disk with the raw uncompressed host-optimal ELF, giving **0.0ms launch overhead**.

---

[**← Advanced Optimizations**](advanced-optimizations.md) | [**Main Index**](../README.md#documentation-guide) | [**Demo & Benchmarks →**](../examples/demo/README.md)
