# Binary Lifecycle Modes

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
| **Command** | Default build artifact | `./app --microfat:trim` | `./app --microfat:optimize` |
| **Disk Size** | `~12.6 MB` | `~6.3 MB` (**-50%**) | `~7.1 MB` |
| **Microarch Portability** | Runs on **any** machine (`v1`–`v4`) | Locked to host level (`v3`) | Locked to host level (`v3`) |
| **Container Auto-Tuning** | ✅ Continuous `GOMEMLIMIT` & `GOMAXPROCS` | ✅ Continuous `GOMEMLIMIT` & `GOMAXPROCS` | ❌ Requires application-level `pkg/cgroup` |
| **In-Memory RAM Exec** | ✅ Anonymous RAM (`memfd_create`) | ✅ Anonymous RAM (`memfd_create`) | ❌ Standard OS disk `mmap` |
| **Read-Only Rootfs** | ✅ Zero disk I/O | ✅ Zero disk I/O | ✅ Native disk read |
| **Startup Latency** | `~1.5 ms` (zstd decompression) | `~1.5 ms` (zstd decompression) | `0.0 ms` (direct kernel exec) |
| **Runtime Execution** | Native `v3` speed (AVX2/FMA) | Native `v3` speed (AVX2/FMA) | Native `v3` speed (AVX2/FMA) |

---

## 3. Recommended Use Cases

### Mode 1: Universal Fat Binary
- **Best For**: GitHub release downloads, multi-machine developer distribution, generic RPM/Deb packages, clusters with heterogeneous CPU hardware (e.g. mix of Intel Skylake and AMD Zen 4 nodes).
- **Workflow**:
  ```bash
  microfat pack --stub bin/microfat-stub -v v1=bin/v1 -v v3=bin/v3 -v v4=bin/v4 -o dist/myapp
  ```

---

### Mode 2: Trimmed Fat Binary (`--microfat:trim`)
- **Best For**: Production container images where container memory and CPU auto-tuning are desired, but image layer size must be minimized.
- **Containerfile Recipe**:
  ```dockerfile
  FROM registry.helsen.dev/ghostnet/distroless:latest

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
  antigravity-up --microfat:optimize

  # Or materialize to explicit target path:
  antigravity-up --microfat:optimize-to ~/.local/bin/antigravity-up
  ```
