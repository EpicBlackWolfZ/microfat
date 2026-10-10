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
| `[✔]` | **Pass**: Capability is available. | None. |
| `[!]` | **Warning**: Functional, but running with sub-optimal configuration (e.g. AVX-512 downclock risk or disk cache fallback). | Review recommended settings below. |
| `[✖]` | **Failure**: Execution is blocked (e.g. unwritable cache AND blocked `memfd_create`). | Follow remediation steps below. |

---

## 2. In-Memory Execution Issues (`memfd_create` and Kernel Memory Sealing)

### Symptom A: `memfd_create failed: operation not permitted` or `permission denied`

#### Root Cause:
A seccomp policy or `vm.memfd_noexec=2` can deny executable memfd creation; kernels before Linux 3.17 lack the syscall. Microfat explicitly requests `MFD_EXEC` so policy scope 1 can permit executable memfds. It retries without that flag only on `EINVAL` for older-kernel compatibility, never on `EPERM` or `EACCES`. Scope 2 remains enforced. See the [kernel policy documentation](https://cdn.kernel.org/doc/html/latest/userspace-api/mfd_noexec.html). No sysctl changes are required or made by Microfat.

---

### Symptom B: `failed to seal memfd descriptor: operation not permitted` (`ErrMemfdSealingFailed`)

#### Root Cause:
The `memfd_create()` syscall succeeded, but `fcntl(fd, F_ADD_SEALS, F_SEAL_WRITE | F_SEAL_SHRINK | F_SEAL_GROW | F_SEAL_SEAL)` was blocked or restricted by the container runtime's seccomp filter, preventing Microfat from sealing the anonymous memory file.

#### Mandatory Memory Sealing Security Contract:
Microfat strictly **prohibits executing unsealed anonymous memory descriptors**. Mandatory seals
prevent writes, resizing and writable shared mappings to the extracted backing file between
verification and execution. They do not provide general process-memory isolation. If sealing fails:

- Under **`MICROFAT_EXEC_MODE=auto` (default)** after a cache miss: The launcher closes the unsealed
  descriptor and can materialize a verified disk cache entry. If memfd followed a warm-cache exec
  failure, it returns both errors without retrying cache.
- Under **`MICROFAT_EXEC_MODE=memfd`**: The launcher strictly enforces in-memory execution and fails fast, terminating the process with an explicit `ErrMemfdSealingFailed` error and full diagnostic context without attempting fallback.

---

### Execution Modes Overview (`MICROFAT_EXEC_MODE`)

| Mode | First choice | Fallback | Cache writes | Error Behavior |
| :--- | :--- | :--- | :--- | :--- |
| **`auto`** *(default)* | Read-only lookup of a verified existing cache entry | Sealed memfd on miss or warm exec failure; cold memfd denial can materialize cache | Only after a cold memfd failure | Unsafe entries and extraction corruption terminate dispatch; warm cache and memfd failures are both reported without cache retry. |
| **`memfd`** | Verified, mandatorily sealed memfd | None | None; bypasses cache lookup | Fails on creation, extraction, sealing or execution errors. |
| **`cache`** | Verified cache descriptor | None | Creates missing cache and repairs corrupt safe regular entries | Rejects unsafe entries and fails if cache cannot execute. |

Every cache hit checks descriptor-bound size, owner, permissions and SHA-256. `MICROFAT_VERIFY_CACHE`
is ignored and cannot disable verification. Missing or corrupt safe regular entries remain untouched
when auto succeeds through memfd. Warm hits avoid decompression; hashing and ELF startup still cost
time. See the [complete dispatch contract](architecture.md#7-cache-first-auto-dispatch--descriptor-bound-execution).

---

### Remediation 1: Update Seccomp Profile (Recommended)
Permit both `memfd_create` and `fcntl` (specifically without restricting `F_ADD_SEALS`) in your Kubernetes or Docker seccomp security profile:

```json
{
  "defaultAction": "SCMP_ACT_ERRNO",
  "architectures": ["SCMP_ARCH_X86_64", "SCMP_ARCH_AARCH64"],
  "syscalls": [
    {
      "names": ["memfd_create", "fcntl"],
      "action": "SCMP_ACT_ALLOW"
    }
  ]
}
```

In Kubernetes Pod Security Context:
```yaml
securityContext:
  seccompProfile:
    type: RuntimeDefault  # RuntimeDefault permits memfd_create and fcntl sealing on modern runtimes
```

---

### Remediation 2: Configure Cache Mode or Auto Fallback
If container security policies restrict in-memory sealing and cannot be modified:
1. Use the default `MICROFAT_EXEC_MODE=auto` to execute a verified prewarmed entry, or materialize
   cache after a cold memfd denial. The cache directory must be writable for cold materialization.
2. Or explicitly set `MICROFAT_EXEC_MODE=cache` to bypass in-memory probing altogether:

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
- Auto first checks a warm cache read-only. A valid executable entry needs no cache write.
- An absent directory/entry or corrupt safe regular entry uses sealed memfd without modifying the
  cache. Successful cold memfd execution does not populate the cache.
- If cold memfd creation, sealing or execution is blocked, disk materialization needs a writable
  cache. It fails when every selected destination is read-only or unavailable.
- If a warm entry cannot execute (for example, its mount is `noexec`), auto tries sealed memfd once.
  If memfd also fails, it reports both attempts without repairing or retrying that entry.

#### Remediation: Prewarm or Mount a Writable `tmpfs`
Prewarm cache for the runtime UID on executable storage before making it read-only, or permit
explicit `MICROFAT_EXEC_MODE=memfd` when immutable extracted storage is required. If policies deny
memfd and the cache may start cold, mount an in-memory `emptyDir` (or `tmpfs`) volume at `/tmp` and
point `MICROFAT_CACHE_DIR` to it:

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
2. Materialized variant binaries are owned by the current UID and created with `0o700`. Every hit
   rechecks descriptor metadata and SHA-256; same-UID writers remain trusted and can mutate the inode.
3. Multi-tenant hosts automatically isolate cache entries per UID into `/tmp/.microfat-<uid>`.

#### Resolving Permission Conflicts:
If a previous execution as `root` created the cache directory with `0700`, subsequent runs as a
non-root user (`UID 10001`) cannot use that directory. Configure a user-private cache for the runtime
UID. Auto does not repair directories during lookup, and symlinks, special files, foreign-owned
entries or unsafe modes are terminal errors rather than a reason to overwrite them.

```bash
# Select a cache directory private to this runtime UID
export MICROFAT_CACHE_DIR="/tmp/microfat-$(id -u)"

# Populate it explicitly before making the cache read-only
./myapp --microfat:prewarm
```

---

## 5. AVX-512 Frequency Downclocking on Intel CPUs

### Symptom: `microfat doctor` emits `[!] Skylake-X / Cascade Lake detected`

#### Root Cause:
On Intel Xeon Family 6 Model 85 processors (Skylake-X, Cascade Lake), sustained AVX-512 work can reduce core frequency. The amount and recovery period vary by processor and workload.

#### Modern Architecture Status:
- **AMD Zen 4 & Zen 5**: The Intel-specific protection rule does not target these processors; measure their actual workload behavior.
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
If steady-state live heap memory exceeds $\frac{\text{GOMEMLIMIT}}{1 + \text{GOGC}/100}$, Go enters continuous GC cycles, saturating the 50% GC CPU limiter.

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
export MICROFAT_GC_PROFILE=batch_etl          # Uses GOGC=off only with a finite effective GOMEMLIMIT

# For Dynamic Formula Sizing:
export MICROFAT_GC_PROFILE=adaptive
export MICROFAT_LIVE_HEAP_ESTIMATE=150MB
```

---

## 7. Tampering & Payload Integrity Verification Failures

> [!NOTE]
> Microfat's verification mechanism detects in-transit corruption, storage degradation, and partial tampering when the trailer/index remains intact. It does not replace public-key publisher signing (e.g. Cosign/GPG) for proving producer authenticity against an adversary who rewrites the entire executable. See [SECURITY.md](../SECURITY.md#6-payload-integrity-vs-producer-authenticity-hashing-vs-signing).

### Common Verification Error Sentinels:

| Error | Root Cause | Remediation |
| :--- | :--- | :--- |
| `invalid microfat magic bytes at EOF` / `missing magic trailer` | Ordinary ELF, truncation, or post-pack ELF rewriting may have removed the packed payload/index/trailer. | Obtain or rebuild a complete artifact; strip inputs before packing. See [packaging guidance](release-artifacts.md#preserve-packed-bytes-during-packaging-and-installation). |
| `index SHA-256 checksum mismatch` | The binary index manifest was modified or corrupted in transit. | Re-download or rebuild fat executable. Run `microfat verify <bin>`. |
| `shared dictionary SHA-256 checksum mismatch` | Embedded Zstandard dictionary was corrupted or truncated. | Verify dictionary size and SHA-256 hash with `microfat inspect <bin>`. |
| `variant payload extends beyond binary boundary` | Binary was truncated during copy or download. | Check complete file size against `stat` and rebuild. |

---

## 8. Frequently Asked Questions (FAQ)

### Q1: Does Microfat add latency to long-running microservices?
Microfat adds startup work, then the payload runs natively without a resident launcher daemon.
Warm auto/cache hits verify and execute existing payload bytes without decompression. Cold memfd
launches extract once per invocation; explicit cache mode or prewarming can retain the extracted
payload for later launches. Hashing, selection, tuning and normal ELF startup still have a cost.

### Q2: Why does `runtime.NumCPU()` still return the host core count?
`runtime.NumCPU()` reports the logical CPUs available at startup, without translating cgroup CPU bandwidth into a core count. Microfat's default `static` policy sets `GOMAXPROCS` from the floor-rounded quota at startup (e.g. `2` for `cpu: 2000m`). Use `MICROFAT_CPU_POLICY=native` to preserve Go's container-aware default and quota/affinity updates when the application's runtime enables them. Explicit environment values, prior setters, and `GODEBUG` settings still take precedence; see the [CPU policy caveats](runtime-tuning.md#b-gomaxprocs-cpu-quota). Throttling remains possible under either policy.

### Q3: How do I eliminate startup overhead entirely for CLI tools?
Use Raw Native ELF mode:
```bash
./my-cli --microfat:optimize
```
This permanently replaces the file on disk with the raw uncompressed selected compatible ELF, removing the microfat launcher stage; native process startup still has a cost.

## 9. Executable Paths, Assets and Deliberate Re-exec

Use the [tested asset-location example](runtime-tuning.md#locating-original-executable--sibling-assets-runtimeinitexecutable) for native, memfd, cache and symlink invocation. A cache executable is a content-addressed payload, not the deployment directory: switching to cache does not make sibling assets or self-update logic based on `os.Executable()` safe.

After unlink/replacement, startup still reads the original kernel-held fat image. The deployment hint may be missing or name a different file. Do not use `MICROFAT_ORIGINAL_EXE` as identity or permission to overwrite it. Launcher transforms reject a detected stale deployment; [serialize transforms and deployment updates](lifecycle-modes.md#4-symlinks--atomic-in-place-operations).

Choose re-exec behavior deliberately:

- To start the **currently deployed release** and repeat CPU selection/tuning, execute an absolute deployment path supplied by trusted application/operator configuration, such as `/opt/myapp/current/app`. Replacement between selection and exec selects whichever complete deployment the kernel opens. Use a versioned immutable path if an exact release is required.
- To restart the **currently running payload** on Linux, `syscall.Exec("/proc/self/exe", os.Args, os.Environ())` executes the kernel-held payload image even if its disk name has gone away. Handle the returned error. This requires accessible procfs and bypasses the fat launcher, so it does not redispatch or recompute launcher tuning. Audit inherited environment and open descriptors for the application's re-exec contract.
- To update software, let a deployment manager verify and atomically install a complete artifact at its configured target. The cache path and original-path hint are not update targets. A native executable has ordinary OS path semantics but still needs an explicit concurrency/update contract.

These choices do not guarantee third-party libraries support memfd paths, deleted files, changed mount views or dynamic assets.
The [mount-layout guide](mount-layouts.md) describes native qualification, procfs failures,
read-only-root cache requirements and companion discovery through mounted generations.

---

[**← Advanced Optimizations**](advanced-optimizations.md) | [**Main Index**](../README.md#documentation-guide) | [**Demo & Benchmarks →**](../examples/demo/README.md)
