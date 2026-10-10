# Native runtime qualification

Issue [#260](https://github.com/EpicBlackWolfZ/microfat/issues/260) requires evidence
from the real launcher on native Linux amd64 and arm64. The qualification controller
is `internal/cmd/runtime-qualify`; its evidence contract is in
`internal/runtimequalify`. It uses the existing disposable mount runner and stays
outside the E2E suite's eager product builds.

## Run locally

Use Go 1.27.2 and Task 3.53.1:

```bash
task qualify-runtime
task qualify-runtime RUNTIME_TESTS=required
task qualify-runtime RUNTIME_TESTS=required RUNTIME_BACKEND=sudo
task test-runtime-fixtures MOUNT_BACKEND=userns
```

The default backend is `userns`. Select `sudo` explicitly on hosts that restrict
unprivileged namespaces; it requires noninteractive sudo authorization. Invoke the
Task as an ordinary user. Both backends create private mount and PID namespaces,
with private propagation and a PID 1 supervisor. Applications have ordinary UID/GID,
no effective capabilities, and `no_new_privs`. Setup and supervision apply only to
disposable fixtures; they never change host sysctls, host mounts, security profiles,
or an existing user's cache.

Prerequisites include native Linux, util-linux `unshare` with PID/mount namespace
support, chroot, procfs, tmpfs, seccomp filters and user notification/CONTINUE,
memfd sealing, pidfds, capability xattrs, and a filesystem allowing ordinary
executable files. The controller checks native kernel architecture and its exact
Go toolchain. Unknown setup failures, output overflow and timeouts fail.

`RUNTIME_TESTS=auto` can record a known unavailable namespace prerequisite as
`incomplete`. This is never a passing qualification. `required` fails on any
missing prerequisite or required result. Each invocation gets a unique directory
under `.work/runtime-qualification`; `RUNTIME_OUTPUT` changes its parent.

## What runs

On each native architecture, the matrix is formats v1/v2, full/minimal launchers,
and none/LZ4/Zstd/Zstd with a shared dictionary: 16 configurations. Dictionary
fixtures use two baseline-compatible payload entries containing identical bytes;
they qualify dictionary handling, without claiming CPU specialization.

For each configuration, explicit cache execution and auto execution under real
`memfd_create` EPERM denial each run three rounds. A round releases 16 workers
together into an empty cache, checks every result and the cache, then releases
another 16 against the warm entry. Configurations run sequentially. These batches
use no syscall tracer or notification supervisor.

Single-worker cases exercise:

| Control or restriction | Required result |
| --- | --- |
| Independent raw memfd/seal probes and unrestricted launchers | Establish working facilities and measure the filter's actual errno |
| Memfd creation EPERM and ENOSYS | Auto uses cache; forced memfd refuses; forced cache works |
| Denied F_ADD_SEALS | Auto uses verified cache; forced memfd refuses |
| Deny first payload exec / every payload exec | Auto falls back once / no application starts |
| Full private 16 MiB tmpfs | Actual ENOSPC, no startup, no staging residue |
| Read-only cache | Forced-cache cold population fails; auto cold memfd leaves it untouched; a valid warm file executes |
| Noexec cache | Forced cache fails; unrestricted auto uses memfd; denied memfd leaves no path |
| Child RLIMIT_NOFILE zero at memfd-create notification | Continued real syscall produces EMFILE; forced memfd and auto fail |
| Normal/noexec/missing/inaccessible procfs | Noexec traversal works; absent access fails at its observed privilege-check stage |
| Verified cache inode held open for writing | Forced cache returns ETXTBSY; auto can use sealed memfd after warm exec failure |
| Actual production memfd | Mandatory seals present; write, shrink, grow and additional-seal attempts fail without changing bytes |
| Corrupt payload/dictionary copies | Extraction fails before startup; a verified warm auto hit does not read or decompress unused source bytes |
| Corrupt warm cache | Auto leaves the safe regular entry untouched and uses verified sealed memfd; explicit cache repairs only from newly verified bytes |
| Symlink, FIFO, unsafe mode, capability-bearing executable | Prompt refusal without application startup |
| Payload exit 42 and SIGTERM | Preserve result, with exactly one startup |

The cache-first policy also has deterministic [dispatch fault tests](../cmd/microfat-stub/cache_first_linux_test.go)
and [full/minimal process regressions](../tests/e2e/cache_first_test.go). They cover read-only cold and
warm lookup, mandatory hit verification, unsafe-entry rejection and cache-to-memfd failure order.
Warm-cache execution denial permits one memfd attempt; if it fails, diagnostics preserve both
attempts without cache retry. Cold auto lookup does not materialize or repair entries unless memfd
creation, sealing or execution fails. Corruption during extraction is terminal. These source tests
do not replace native or authenticated candidate qualification.

The reporter independently hashes its executable and records PID, credentials,
arguments, stdin, environment and descriptor targets. Checks require the selected
tier and image to stay unchanged under policy failures. Cache snapshots verify
the final hash, size, owner, executable permissions and absence of staging files.
Payloads may retain their ordinary standard streams and Go runtime event
descriptors; launcher, staging, policy and synchronization descriptors must close.

Seccomp programs check the syscall architecture. Notification handling permits
bootstrap execution, then pins the descriptor selected for payload execution and
checks mandatory seals and rejected mutation attempts before replying. Writable
probe handles close before the reply so they cannot cause ETXTBSY. Hashing runs
after the reply against the pinned read-only descriptor, which survives payload
exec/exit and descriptor closure. Its digest must match the independently known
payload digest; any inspection failure still invalidates the case. This prevents
Go preemption from repeatedly cancelling a slow full-CLI hash while exec waits.
It does not emulate successful syscalls or write application memory. Kernel-confirmed
cancelled notification IDs are recorded with a bounded limit; no application is
retried. This is a test synchronization mechanism, not a hostile-process security
boundary. See the [kernel seccomp documentation](https://www.kernel.org/doc/html/latest/userspace-api/seccomp_filter.html).
The busy-inode case uses actual [execve ETXTBSY](https://man7.org/linux/man-pages/man2/execve.2.html),
without the E2E retry helper.

Each child has a 30-second limit, batches a 60-second limit, and stdout/stderr
a 256 KiB limit each. Startup reporting is also bounded. Overflow invalidates
evidence. Killing namespace PID 1 terminates even detached descendants. Required
native source CI separately runs the timeout/detached-child regression with
pidfds to verify teardown, plus full/minimal fat CLI execution in all three modes.
The real CLI is padded to 64 MiB for this regression so inspection exceeds the
preemption interval even on fast runners; asynchronous preemption stays enabled.

## Authenticated candidates

Candidate qualification requires the exact source checkout, with no tracked or
untracked changes, and a downloaded draft inventory. For example, after the
maintainer stages a signed draft:

```bash
tag=v0.3.0
dist="$PWD/.work/runtime-dist"
mkdir -p "$dist"
release_id=$(gh release view "$tag" --repo EpicBlackWolfZ/microfat --json databaseId --jq .databaseId)
gh api "repos/EpicBlackWolfZ/microfat/releases/$release_id" > "$dist/release.json"
gh release download "$tag" --repo EpicBlackWolfZ/microfat --dir "$dist" \
  --pattern 'microfat_*' --pattern 'microfat-install_*' --pattern 'checksums.txt*'
task qualify-runtime RUNTIME_INPUT=candidate RUNTIME_TESTS=required \
  RUNTIME_BACKEND=sudo RUNTIME_DIST="$dist" RUNTIME_TAG="$tag" \
  RUNTIME_SOURCE="$(git rev-parse HEAD)"
```

Install the release workflow's pinned Cosign before this command. Authentication
reuses the release audit's exact workflow/tag identity, GitHub OIDC issuer,
workflow source SHA, signed inventory, checksums and safe extraction. No downloaded
product executes before authentication. The controller never builds a replacement
CLI or launcher in candidate mode. Only fixture helpers/reporters are built locally.

The unmodified distributed fat CLI runs in all three dispatch modes. That same
authenticated CLI packs the matrix using the authenticated full/minimal stubs.
Those bundles are **derived test fixtures**, not independently signed assets.
Their lineage records the parent archive hash, stub hash, reporter hash, packing
arguments and bundle hash. Separately copied corrupt fixtures have their own
executed hash. Source runs instead retain the revision, dirty state, patch and
untracked files, build commands/settings, and product hashes.

## Evidence and replay

The versioned `summary.json` contains the complete expected-case manifest before
acquisition starts. It is updated after each case, alongside per-case JSON,
`commands.jsonl`, products, helpers, derived bundles and lineage records.
Authentication inputs are retained under `authentication/`. Every case records
the actual namespace/mappings/mounts, cache snapshots, policy observations,
worker startup/stdout/stderr/exit/signal/timeout/duration, and the exact invocation.
Missing, duplicate, unexpected, skipped, failed or malformed required cases
invalidate a complete summary.

Successful disposable fixture roots are removed. On failure, the first failing
root and request remain, and later repetitions cannot turn that run green.
The failing case JSON's `command` is the minimized invocation: execute its argument
array using the same backend and retained helper/request. Recreate any modified
fixture from its retained bundle before another attempt. For a fresh complete
reproduction, check out the recorded source, apply `source.patch` and retained
`untracked-source/` if present, then repeat the recorded Task command. Candidate
replays require the same authenticated distribution. Keep the original failure
evidence when comparing a proposed fix.

Native source PR results, authenticated candidate results, and
[QEMU evidence](qemu-qualification.md) have distinct labels and contracts. Finite
stress runs establish observed behavior for their kernels, filesystems and
policies; they are not a proof of race freedom or a survey of every LSM/container
configuration. [Mount qualification](mount-layouts.md) remains a separate suite.

## CI and publication

Code-changing PRs, pushes and manual CI runs require the complete source matrix
on hosted native amd64 and arm64. Documentation-only classification is preserved.
Both matrix legs use explicit sudo, `fail-fast: false`, a 45-minute timeout, and
30-day evidence retention with unconditional uploads. `CI Complete` includes
this requirement.

For v0.3.0 and later, including prereleases, the release producer calls
`runtime-qualification.yml` after staging its signed draft. These native jobs have
read-only repository permissions and no publishing credentials.
The producer, which already has draft access, downloads the exact draft assets,
signed checksums and release metadata and uploads an artifact named for its run
and attempt. Both runtime and installer qualification consume that artifact and
authenticate the received bytes before execution. They do not query unpublished
releases with read-only tokens; GitHub requires push access to see drafts.

Both normal publication and manual recovery use the same finalizer. It requires
successful jobs and complete candidate summaries from the exact producer run
attempt, verifies tag/source/architecture and asset IDs/digests, and rechecks
the current draft inventory immediately before publication. Source-mode,
missing, skipped, mixed-attempt, stale or substituted evidence fails closed.
Verified runtime evidence is attached as a separate supplemental archive and
checksum, without changing the signed product inventory or benchmark archive
contract. Historical release requirements remain unchanged.

Implementation and green source CI alone do not complete #260. Its acceptance
still requires merged gates and passing native results for both architectures
against the same authenticated signed candidate. Release creation, publication
and PR merge remain maintainer-controlled.
