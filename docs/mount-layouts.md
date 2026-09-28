# Executable paths in mounted deployments

Microfat reads its kernel-held executable image during startup. The pathname exported
as `MICROFAT_ORIGINAL_EXE`, and returned by `runtimeinit.Executable()`, is an
informational location in the process's filesystem view. It is not image identity
or permission to replace a file. See the [asset-location example](runtime-tuning.md#locating-original-executable--sibling-assets-runtimeinitexecutable).

## Supported layouts and expected failures

| Layout | Behavior |
| --- | --- |
| Directory bind mount with neighboring assets | Direct, symlink and PATH invocation resolve the physical mounted executable location. Spaces in names are preserved. |
| Individual executable bind mount | The mounted inode remains selected even if its underlying source pathname is replaced. Mount neighboring assets and companion stubs separately when needed. |
| Read-only deployment directory | Launching does not require writing beside the fat executable. Packing requires a separate writable output destination. |
| Read-only root with accessible procfs | Explicit memfd dispatch works without a writable deployment. Explicit cache dispatch needs usable executable cache storage. |
| Read-only root and no writable cache storage | An explicit cache cold miss fails before payload startup. It does not switch to memfd. |
| Procfs mounted `noexec` | This does not by itself prevent traversal through `/proc/self/fd` to an executable target. It is different from an inaccessible or missing `/proc`. |
| Missing or inaccessible procfs | The launcher fails closed. Its secure-execution check reads `/proc/self/auxv` before opening or dispatching the payload. |
| Broken entrypoint link or missing deployment mount | The kernel or invoking program cannot start the launcher. This is separate from a launcher diagnostic. |
| Unlink/replacement after the kernel loads the launcher | The original payload still runs. The exported deployment pathname can be absent or name a replacement; adjacent assets can belong to the replacement. |

For a read-only container root, supply an ordinary-user-owned executable cache mount
and set `MICROFAT_CACHE_DIR` to its path when using cache mode. Keep procfs accessible.
An explicit, stable versioned asset directory is the appropriate choice when code
and assets must stay paired across replacement.

Automatic companion discovery requires the matching `microfat-stub` and
`microfat-stub-minimal` files. A file-only CLI mount does not bring its siblings
into the container. Missing companions with no eligible PATH alternative cause
packing to fail without producing a new output.

For installer-owned generations, preserve the absolute store/bin paths recorded in
ownership metadata and expose the complete physical generation. A G1 process keeps
using G1 companions after G2 activation. Removing G1 products or metadata causes
managed discovery to fail rather than select G2 or unrelated PATH companions.
Mounting a managed store at a different pathname does not rewrite its ownership
contract. See [installation discovery](design/installation-discovery-contract.md).

## Native qualification and replay

From a source checkout with the documented Go/Task prerequisites:

```bash
task qualify-mounts
task qualify-mounts MOUNT_TESTS=required MOUNT_BACKEND=userns
```

The default backend uses an unprivileged user namespace while retaining the
invoking UID/GID. It requires util-linux `unshare` with `--map-current-user` and
`--keep-caps`, and host support for user/mount namespaces, chroot and ptrace.
Setup capabilities are removed before the tested executable runs. It never
automatically invokes sudo. Hosted native CI explicitly selects
`MOUNT_BACKEND=sudo`; only namespace setup is privileged, and payloads run as the
ordinary runner user. There is no container-engine dependency.

`MOUNT_TESTS=auto` records an unavailable namespace prerequisite as a skip and
prints an incomplete-qualification result. `required` fails on unavailable
prerequisites; setup errors, failed assertions, incomplete results and timeouts
always fail. Once namespace setup is available, unsupported fixture operations
are failures to investigate, not silently accepted skips.

Each invocation writes a separate run directory beneath `.work/mount-qualification`
(override with `MOUNT_OUTPUT`). The versioned `summary.json` enumerates all expected
cases and their results. Per-case JSON includes requests, actual mount flags,
namespace mappings, payload hashes, process output, exit status and commands.
`tests.log` also records the harness rejection and cleanup checks. Source-built
products and their digests are retained. Replay by checking out the recorded source
commit and rerunning the same Task command/backend; local dirty-tree evidence is
explicitly marked and requires retaining those source changes too.

The matrix covers native Linux amd64/arm64, full/minimal launchers, formats v1/v2,
none/LZ4/Zstd/shared-dictionary compression, explicit memfd, and cold/warm cache.
Dictionary fixtures use two tier entries containing the same baseline-compatible
payload; they test dictionary dispatch, not microarchitecture specialization.
Managed companion tests use the format-v2 installation contract. Raw static
executables provide independent controls for procfs absence.

These are source-built functional checks with static Go payloads. They do not
qualify every dynamic loader, container security policy, filesystem, concurrent
writer or emulator. They do not authenticate a release candidate or establish
performance claims. QEMU descriptor qualification (#231) and concurrent/policy
qualification against authenticated candidate artifacts (#260) remain separate.
