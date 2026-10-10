# Binary Lifecycle Modes

[**← Container Runtime Tuning**](runtime-tuning.md) | [**Main Index**](../README.md#documentation-guide) | [**Advanced Optimizations →**](advanced-optimizations.md)

---

Microfat supports three distinct binary operational modes to accommodate different stages of the application lifecycle, from multi-architecture release distribution to tight container image optimization and raw host performance.

All modes inherit the [documented trust boundary](../SECURITY.md#8-threat-model-and-enforcement-map).
Authenticate the original distribution before first execution. Trimming or optimizing changes the
artifact bytes: the original whole-archive signature does not authenticate the transformed output.
Verify the result and establish a new trusted digest/signature when distributing it onward.

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
  │ Retains stub + single selected v3  │         │ Strips stub; raw uncompressed v3  │
  │     Size: depends on codec     │         │        Size: native payload     │
  │ Auto-tunes cgroup & cache/memfd    │         │ Zero launcher overhead (mmap)     │
  └───────────────────────────────────┘         └───────────────────────────────────┘
```

---

## 2. Comprehensive Modes Comparison

| Characteristic | 1. Universal Fat Binary | 2. Trimmed Fat Binary (`--trim`) | 3. Raw Native ELF (`--optimize`) |
| :--- | :--- | :--- | :--- |
| **Command** | Default build artifact | `./app --microfat:trim` or `microfat trim app` | `./app --microfat:optimize` |
| **Disk Size** | Stub + all compressed variants | Stub + selected compressed variant | Selected uncompressed payload |
| **Microarch Portability** | Runs on **any** machine (`v1`–`v4` or `v8.0`–`v9.5`) | Locked to chosen level (e.g. `v3`) | Locked to chosen level (e.g. `v3`) |
| **Container Auto-Tuning** | ✅ Startup memory/GC tuning; static or native CPU policy | ✅ Startup memory/GC tuning; static or native CPU policy | Requires standalone `runtimeinit` or `runtimeinit/autoload` for Microfat tuning |
| **In-Memory RAM Exec** | ✅ Sealed memfd on an auto miss or when explicitly requested | ✅ Sealed memfd on an auto miss or when explicitly requested | ❌ Standard OS disk `mmap` |
| **Read-Only Rootfs** | Verified warm cache or permitted sealed memfd; cold cache fallback needs writable storage | Verified warm cache or permitted sealed memfd; cold cache fallback needs writable storage | ✅ Native disk read |
| **Startup Work** | Select, verify warm cache or extract payload, tune and execute | Verify warm cache or extract payload, tune and execute | Native process startup |
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
- **Best For**: Developer workstations, sub-millisecond CLI utilities (like shell prompt generators), or fixed bare-metal servers where a native executable without a launcher stage is desired.
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
- On Linux, invocation through a symbolic link (e.g. `/usr/local/bin/app -> /opt/app/bin/app_fat`) resolves to the physical executable. In-place transformations replace that physical deployment file while preserving permissions; the symlink remains in place.
- If the transformation still receives a symlink path that needs resolution, it prints the canonical target path:
  ```
  [microfat] Notice: resolved symlink '/usr/local/bin/app' -> target '/opt/app/bin/app_fat'
  ```
- To create a specialized binary without modifying the original or symlinked file, use explicit destination commands:
  ```bash
  ./app --microfat:trim-to /path/to/trimmed-binary
  ./app --microfat:optimize-to /path/to/extracted-elf
  ```

Before any `trim`, `specialize`, `optimize`, or corresponding `-to` command, the launcher checks that the original deployment path still identifies its open running image. If the deployment was unlinked or replaced, the command fails without transforming the newer file. Restart from the intended deployment before transforming it. Read-only `--microfat:info` continues to describe the original image.

### Transactional Lifecycle & Metadata Policies

All transformation operations (both CLI `microfat trim` and launcher stub meta-commands) execute within a shared, transactional lifecycle engine:

1. **Create-Only Publication for Fresh Outputs**:
   - Commands targeting a new destination (`-o <path>`, `--microfat:trim-to`, `--microfat:optimize-to`) enforce create-only semantics via `renameat2(..., RENAME_NOREPLACE)`.
   - If the destination already exists (regular file, symlink, FIFO, directory), the operation immediately aborts without overwriting or truncating the target.

2. **Writer Serialization via Advisory Locks**:
   - In-place transformations acquire an exclusive non-blocking advisory write lock (`flock(fd, LOCK_EX | LOCK_NB)`) on the source executable before snapshotting metadata and staging changes.
   - Concurrent `microfat` mutations return `ErrConcurrentTransformation` immediately instead of producing interleaved or corrupted files.

3. **Hard-Link Protection**:
   - In-place mutation of files with multiple directory entries (`nlink > 1`) is strictly rejected by default (`ErrHardLinkDetected`) to prevent silently altering other paths that reference the same inode.
   - Operators can pass `--break-hardlinks` (CLI) or `--microfat:break-hardlinks` (launcher) to deliberately sever the hard link, writing the derivative to a new inode while preserving untouched aliases at the original inode.

4. **Explicit Metadata Policies (`--metadata-policy=strict|strip`)**:
   - **`strict` (Default)**: Preserves source file ownership (`chown`), file permissions (`chmod`), and supported extended attributes (`user.*`, `security.selinux`). Strictly rejects files with `setuid`/`setgid` bits, executable capabilities (`security.capability`), or content-bound integrity metadata (`security.ima`, `security.evm`).
   - **`strip`**: Deliberately creates a standard caller-owned derivative (`0755` masked with source permissions) and strips non-policy extended metadata and ACLs.

5. **Deployment Coordination & Integrity Boundary**:
   - In-place operations recheck source identity (`dev`, `ino`, `size`, `modtime`) prior to final atomic rename.
   - Note that Linux `rename` is an atomic directory update, not a kernel compare-and-swap (CAS). External deployers that do not participate in the `flock` protocol must be quiesced during maintenance windows.
   - Transformed derivatives contain new composite bytes and do not inherit external signatures; re-sign derivatives using your organization's deployment signing infrastructure before distribution. See [Security Policy](../SECURITY.md#6-payload-integrity-vs-producer-authenticity-hashing-vs-signing).

---

## 5. Node Cache Prewarming (`--microfat:prewarm` / `microfat prewarm`)

In cold-start sensitive environments (e.g. serverless containers, Kubernetes `initContainers`, node
boot scripts, or golden AMI images), pre-extracting the binary avoids decompression on a verified
warm hit while preserving universal multi-variant distribution. Default auto mode discovers a
prewarmed entry read-only; no execution-mode override is needed. Cache hashing, launcher work and
normal ELF startup still apply. A successful cold auto memfd launch does not populate this cache.

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
         Runtime Launch with        │ Verify descriptor, then execve
         auto (default) or cache    │ + Full cgroup auto-tuning
                                    ▼
       ┌────────────────────────────────────────────────────────┐
       │            Running Optimal Microarch Process           │
       └────────────────────────────────────────────────────────┘
```

### CLI Command
```bash
# Prewarm selected compatible variant into cache:
microfat prewarm /usr/local/bin/myapp

# Prewarm all variants into cache (e.g. for shared multi-tenant cache partitions):
microfat prewarm --all --cache-dir /var/cache/microfat /usr/local/bin/myapp

# Verify cache health without extracting:
microfat prewarm --verify /usr/local/bin/myapp

# Structured JSON output:
microfat prewarm --json /usr/local/bin/myapp
```

Verification opens existing cache directories without creating them or repairing permissions, and never extracts or removes entries. Missing or insecure explicit directories fail directly. Automatic discovery tries the configured XDG/home and temporary-directory candidates without modifying any of them; if none is secure and present it fails. Missing or corrupt entries produce a failing exit status and remain untouched. This contract also applies to the full launcher's verify meta-command and library cache verification with implicit directory selection.

Auto dispatch also starts with read-only discovery, but missing or corrupt safe regular entries
proceed to sealed memfd. Unsafe entries are rejected without repair. Prewarming and explicit cache
mode can materialize missing entries and repair corrupt safe regular entries. Every launch verifies
the selected descriptor's size, owner, mode and SHA-256. A valid warm hit does not re-decompress the
embedded source payload or dictionary; authenticate the whole artifact before deployment.
Cache entries remain mutable by trusted same-UID writers. See the
[dispatch policy](architecture.md#7-cache-first-auto-dispatch--descriptor-bound-execution).


### Launcher Stub Hook
```bash
# Decompress selected compatible variant and exit 0 immediately without running the app:
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
          value: "auto"
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
Environment=MICROFAT_EXEC_MODE=auto
ExecStartPre=/usr/local/bin/myapp --microfat:prewarm
ExecStart=/usr/local/bin/myapp --port 8080
Restart=always
```

---

[**← Container Runtime Tuning**](runtime-tuning.md) | [**Main Index**](../README.md#documentation-guide) | [**Advanced Optimizations →**](advanced-optimizations.md)

## ARM64 user-mode emulation

See the [QEMU descriptor-execution qualification](qemu-qualification.md) for the
F-only interpreter limitation, full-profile `optimize-to` extraction route, and
minimal-profile restrictions. Extraction is explicit and changes the executable
into a single selected raw payload; it is not an automatic dispatch fallback.
