# Binary Lifecycle Modes

[**← Container Runtime Tuning**](runtime-tuning.md) | [**Main Index**](../README.md#documentation-guide) | [**Advanced Optimizations →**](advanced-optimizations.md)

---

Microfat supports three distinct binary operational modes to accommodate different stages of the application lifecycle, from multi-architecture release distribution to tight container image optimization and raw host performance.

---

## 1. Lifecycle Modes Overview

```
                      ┌─────────────────────────────────────────┐
                      │    1. Universal Fat Binary (Default)    │
                      │  Contains all microarch levels (v1..v4)  │
                      │         Size: ~12.6 MB (Combined)       │
                      └────────────────────┬────────────────────┘
                                           │
                    ┌──────────────────────┴──────────────────────┐
                    │                                             │
                    ▼                                             ▼
  ┌───────────────────────────────────┐         ┌───────────────────────────────────┐
  │   2. Trimmed Fat Binary (--trim)  │         │ 3. Raw Native ELF (--optimize)    │
  │ Retains stub + single optimal v3  │         │ Strips stub; raw uncompressed v3  │
  │     Size: ~6.3 MB (-50% disk)     │         │        Size: ~7.1 MB (Native)     │
  │ Auto-tunes cgroup & RAM memfd     │         │ Zero launcher overhead (mmap)     │
  └───────────────────────────────────┘         └───────────────────────────────────┘
```

---

## 2. Comprehensive Modes Comparison

| Characteristic | 1. Universal Fat Binary | 2. Trimmed Fat Binary (`--trim`) | 3. Raw Native ELF (`--optimize`) |
| :--- | :--- | :--- | :--- |
| **Command** | Default build artifact | `./app --microfat:trim` or `microfat trim app` | `./app --microfat:optimize` |
| **Disk Size** | `~12.6 MB` | `~6.3 MB` (**-50%**) | `~7.1 MB` |
| **Microarch Portability** | Runs on **any** machine (`v1`–`v4` or `v8.0`–`v9.5`) | Locked to chosen level (e.g. `v3`) | Locked to chosen level (e.g. `v3`) |
| **Container Auto-Tuning** | ✅ Continuous `GOMEMLIMIT` & `GOMAXPROCS` | ✅ Continuous `GOMEMLIMIT` & `GOMAXPROCS` | ❌ Requires standalone `runtimeinit` import |
| **In-Memory RAM Exec** | ✅ Anonymous RAM (`memfd_create`) | ✅ Anonymous RAM (`memfd_create`) | ❌ Standard OS disk `mmap` |
| **Read-Only Rootfs** | ✅ Zero disk I/O | ✅ Zero disk I/O | ✅ Native disk read |
| **Startup Latency** | `~1.5 ms` (zstd decompression) | `~1.5 ms` (zstd decompression) | `0.0 ms` (direct kernel execve) |
| **Runtime Execution** | Native hardware speed (AVX2/FMA/SVE) | Native hardware speed (AVX2/FMA/SVE) | Native hardware speed (AVX2/FMA/SVE) |

---

## 3. Recommended Use Cases

### Mode 1: Universal Fat Binary
- **Best For**: GitHub release downloads, multi-machine developer distribution, generic RPM/Deb packages, clusters with heterogeneous CPU hardware (e.g. mix of Intel Skylake and AMD Zen 4 nodes).
- **Workflow**:
  ```bash
  microfat pack --stub bin/microfat-stub -v v1=bin/v1 -v v3=bin/v3 -v v4=bin/v4 -o dist/myapp
  ```

---

### Mode 2: Trimmed Fat Binary (`--microfat:trim` / `microfat trim`)
- **Best For**: Production container images where container memory and CPU auto-tuning are desired, but image layer size must be minimized.
- **Containerfile Recipe**:
  ```dockerfile
  FROM gcr.io/distroless/static-debian12:nonroot

  # Copy the universal binary
  COPY --from=builder /build/dist/myapp /usr/local/bin/myapp

  # Trim unneeded variants during image build, locking to the target architecture
  RUN /usr/local/bin/myapp --microfat:trim

  ENTRYPOINT ["/usr/local/bin/myapp"]
  ```

---

### Mode 3: Raw Native ELF (`--microfat:optimize`)
- **Best For**: Developer workstations, sub-millisecond CLI utilities (like shell prompt generators), or fixed bare-metal servers where absolute zero launcher latency is desired.
- **Workflow**:
  ```bash
  # In-place specialization:
  ./myapp --microfat:optimize

  # Or materialize to explicit target path:
  ./myapp --microfat:optimize-to ~/.local/bin/myapp
  ```

---

## 4. Symlinks & Atomic In-Place Operations

When executing in-place mutations (`--microfat:trim` or `--microfat:optimize`):
- If the binary is invoked via a symbolic link (e.g. `/usr/local/bin/app -> /opt/app/bin/app_fat`), Microfat evaluates the symlink (`filepath.EvalSymlinks`) to atomically replace the physical destination binary while preserving permissions.
- Microfat explicitly prints a notification indicating the canonical target path being replaced:
  ```
  [microfat] Notice: resolved symlink '/usr/local/bin/app' -> target '/opt/app/bin/app_fat'
  ```
- To create a specialized binary without modifying the original or symlinked file, use explicit destination commands:
  ```bash
  ./app --microfat:trim-to /path/to/trimmed-binary
  ./app --microfat:optimize-to /path/to/extracted-elf
  ```

---

## 5. Node Cache Prewarming (`--microfat:prewarm` / `microfat prewarm`)

In cold-start sensitive environments (e.g. serverless containers, Kubernetes `initContainers`, node boot scripts, or golden AMI images), pre-extracting the decompressed binary eliminates decompression latency on first execution while still preserving universal multi-variant fat binary distribution.

### Prewarming Mechanics

```
                 ┌──────────────────────────────────────┐
                 │      Universal Fat Binary (v1..v4)   │
                 └──────────────────┬───────────────────┘
                                    │
                       Prewarm Hook │ (Decompress once)
                                    ▼
       ┌────────────────────────────────────────────────────────┐
       │   Local Node Cache: $XDG_CACHE_HOME/microfat/<SHA256>  │
       │           (or custom $MICROFAT_CACHE_DIR)              │
       └────────────────────────────┬───────────────────────────┘
                                    │
         Runtime Launch with        │ Direct execve (0 decompression overhead)
         MICROFAT_EXEC_MODE=cache   │ + Full cgroup auto-tuning
                                    ▼
       ┌────────────────────────────────────────────────────────┐
       │            Running Optimal Microarch Process           │
       └────────────────────────────────────────────────────────┘
```

### CLI Command
```bash
# Prewarm host-optimal variant into cache:
microfat prewarm /usr/local/bin/myapp

# Prewarm all variants into cache (e.g. for shared multi-tenant cache partitions):
microfat prewarm --all --cache-dir /var/cache/microfat /usr/local/bin/myapp

# Verify cache health without extracting:
microfat prewarm --verify /usr/local/bin/myapp

# Structured JSON output:
microfat prewarm --json /usr/local/bin/myapp
```

### Launcher Stub Hook
```bash
# Decompress host-optimal variant and exit 0 immediately without running the app:
/usr/local/bin/myapp --microfat:prewarm

# Decompress all variants:
/usr/local/bin/myapp --microfat:prewarm=all

# Verify cache health:
/usr/local/bin/myapp --microfat:prewarm=verify
```

---

## 6. Cold-Start Optimization Recipes

### A. Kubernetes `initContainer` Recipe
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: myapp-pod
spec:
  initContainers:
    - name: prewarm-cache
      image: myapp:latest
      command: ["/usr/local/bin/myapp", "--microfat:prewarm"]
      volumeMounts:
        - name: app-cache
          mountPath: /root/.cache/microfat
  containers:
    - name: myapp
      image: myapp:latest
      env:
        - name: MICROFAT_EXEC_MODE
          value: "cache"
      volumeMounts:
        - name: app-cache
          mountPath: /root/.cache/microfat
  volumes:
    - name: app-cache
      emptyDir: {}
```

### B. Systemd Unit `ExecStartPre` Hook
```ini
[Unit]
Description=High Performance Microfat Service
After=network.target

[Service]
Environment=MICROFAT_CACHE_DIR=/var/cache/microfat
Environment=MICROFAT_EXEC_MODE=cache
ExecStartPre=/usr/local/bin/myapp --microfat:prewarm
ExecStart=/usr/local/bin/myapp --port 8080
Restart=always
```

---

[**← Container Runtime Tuning**](runtime-tuning.md) | [**Main Index**](../README.md#documentation-guide) | [**Advanced Optimizations →**](advanced-optimizations.md)
