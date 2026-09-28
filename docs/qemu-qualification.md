# ARM64 QEMU descriptor-execution qualification

Native Linux amd64 and arm64 remain the supported execution baseline. ARM64
user-mode emulation has an additional interpreter contract: successful execution
of an ordinary ARM64 pathname does not establish that descriptor-backed dispatch
works under the same binfmt registration.

Issue [#231](https://github.com/EpicBlackWolfZ/microfat/issues/231) records this
distinction for QEMU 10.2.2 on Linux 7.2.6 with an enabled `F`-only registration.
The source-built qualification reproduces that configuration in private namespaces;
its evidence records the exact kernel, emulator version and executable digest used.
It does not extend that result to every QEMU version or registration.

## Compatibility contract

| Execution path | Expected result in the qualified F-only environment |
| --- | --- |
| Raw static ARM64 ELF, explicit `qemu-aarch64-static` invocation | Payload executes. |
| Raw static ARM64 ELF, ordinary pathname through binfmt | Payload executes. |
| ARM64 descriptor probe with `FD_CLOEXEC`, disk or sealed memfd | Interpreter fails before payload startup. |
| Same probe with its descriptor deliberately retained | Payload executes; retained descriptor is reported. This is a diagnostic only. |
| Full/minimal fat ARM64 launcher, explicit memfd or cold/warm cache | Verified dispatch reaches the execution boundary, then fails before payload startup. |
| Fat ARM64 launcher, current memfd-first auto mode | Same descriptor limitation; auto is not an emulator workaround. |
| Native descriptor probe with `FD_CLOEXEC` | Payload executes and the original execution descriptor is closed. |

The kernel's [`F` flag](https://github.com/torvalds/linux/blob/v6.8/Documentation/admin-guide/binfmt-misc.rst)
opens and retains the **interpreter** at registration time, making it available
across mount namespaces and chroots. It does not preserve the application's
close-on-exec descriptor. With this registration, the new interpreter attempts to
open the descriptor pathname after the kernel has closed that descriptor.

The original launcher has already been replaced when the interpreter fails.
There is no launcher left to retry another mode. Explicit cache still executes a
verified descriptor; it does not bypass this limitation by executing a mutable
pathname. No launcher descriptors are deliberately leaked and mandatory memfd
seals, image verification and native argument/PID fidelity remain unchanged.

`F` alone also does not promise preservation of an arbitrary `argv[0]` across
interpreter dispatch. The diagnostic control records the descriptor pathname
passed to the interpreted payload; native argument assertions stay strict.

### Explicit extraction with a full launcher

For a full-profile fat executable in the qualified environment:

```bash
./app --microfat:optimize-to=./app-raw
./app-raw
```

The full launcher's meta-command verifies and extracts the selected payload
without dispatching it. Use a fresh, explicit output path and keep the original
fat executable. The raw result has no launcher, runtime auto-tuning or portable
variant selection; it contains the selected CPU tier and retains any original
loader/library requirements. See [lifecycle modes](lifecycle-modes.md).

Minimal launchers reject meta-commands, including `optimize-to`. Their raw-path
qualification uses the separately built, hash-matched source payload. Do not
clear `CLOEXEC` globally or modify host binfmt registrations to make a launch pass.

## Running the qualification

Prerequisites are native Linux amd64, the repository's Go 1.27.1 and Task 3.53.1,
a static native `qemu-aarch64-static`, util-linux `unshare`, and kernel support for
user, mount and PID namespaces with user-namespace-owned `binfmt_misc`. Standard
Git, tar and Bash are used to retain source changes and logs. The explicit sudo
backend additionally uses `setpriv` and a native test bootstrap that writes exact
root/ordinary-user UID/GID maps through Go's process API. It does not depend on
`newuidmap` grants or change host subordinate-ID configuration.

```bash
task qualify-qemu
task qualify-qemu QEMU_TESTS=required QEMU_AARCH64=/path/to/qemu-aarch64-static
task qualify-qemu QEMU_TESTS=required QEMU_BACKEND=sudo
```

`QEMU_BACKEND=userns` is the default and never invokes sudo. `sudo` is explicitly
selected in hosted CI for namespace setup; the emulated application still runs
with ordinary user credentials, no effective capabilities and `no_new_privs`.
Both backends create fresh user, mount and PID namespaces. A mount namespace alone
does not isolate binfmt registrations. The runner registers only in a newly
mounted empty instance, and parent registration snapshots must remain unchanged.

The CI job downloads an Ubuntu static QEMU package through authenticated APT
metadata, checks its SHA-256 and extracts it without installation or maintainer
scripts. It records the resolved package version and hashes. It never installs
host-wide handlers. This emulator provenance is separate from authentication of
microfat release artifacts.

`QEMU_TESTS=auto` records known unavailable prerequisites as skips and leaves
qualification incomplete. `required` fails on those skips. Setup errors,
corruption, failed positive controls, timeouts and unexpected outcomes always fail.

The native controller uses the race detector. Static ARM64 fixtures target
`GOARM64=v8.0`; the race runtime is not run under QEMU. The matrix includes both
formats, full/minimal profiles, and none/LZ4/Zstd/shared-dictionary compression:
16 configurations and 64 launch observations, plus eight controls and sixteen
lifecycle cases. Dictionary entries deliberately share baseline-compatible bytes;
they do not qualify CPU-tier specialization. Full-profile extraction cases also
execute and verify the resulting raw payload.

## Evidence and replay

Each run creates a unique directory under `.work/qemu-qualification` (override
with `QEMU_OUTPUT`). `summary.json` lists all 88 expected cases and their outcomes.
Per-case JSON records requests, commands, namespace mappings, registration and
mount properties, output, exit status, startup reports, dispatch metadata, hashes,
cache snapshots and duration. `tests.log` also retains fixture guard and timeout
cleanup results, including detached descendants.

A passing case with outcome `expected-descriptor-limitation` means that the
limitation was reproduced. **It does not mean the payload ran successfully.**
Classification requires successful independent controls, a verified product and
correct dispatch telemetry before exit 1 without payload startup or stdout. Stderr
must contain exactly the expected pre-exec telemetry record; extra diagnostics,
including panics and runtime failures, invalidate launcher and probe observations.
Arbitrary failures are not accepted as reproductions. Unexpected success requires
review of the changed compatibility contract rather than automatic acceptance.

The evidence retains build products and exact emulator bytes. Replay by checking
out the recorded revision, restoring `source.patch` and any archived untracked
source for a dirty run, then running the same Task command with that emulator.
Record the replay's kernel/environment separately; matching source and emulator
bytes alone do not recreate kernel policy. Requests contain temporary fixture paths;
rerun the Task to reconstruct the disposable fixtures.

Native [mount qualification](mount-layouts.md), this emulator result, the historical
authenticated v0.2.4 audit, and future authenticated candidate qualification under
#260 are separate evidence. This suite does not qualify additional binfmt flags,
dynamic loader combinations, all filesystem/security policies, or release assets.
