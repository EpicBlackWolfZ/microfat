# Installation ownership and companion discovery

Schema 1 is implemented for the v0.3.0 verified Linux installer ([#203](https://github.com/EpicBlackWolfZ/microfat/issues/203)).
The updater (#204) consumes this contract; Linux Homebrew integration (#205) uses the separate
external-distribution marker below.
See [installation](../installation.md) for commands and [release verification](../release-verification.md) for publisher trust.

## Managed layout

The default entrypoints are `$HOME/.local/bin`. The default store is
`${XDG_DATA_HOME:-$HOME/.local/share}/microfat/installations/default`.
Explicit roots must be clean, absolute, nonoverlapping paths, with no symlink ancestors.
Root execution requires `--system` and both roots; the installer never invokes sudo.

```text
store/
  owner.json
  .lock
  generations/<32-lowercase-hex-ID>/
    microfat
    microfat-stub
    microfat-stub-minimal
    generation.json
  current -> generations/<ID>
bin/microfat -> store/current/microfat
bin/microfat-stub -> store/current/microfat-stub
bin/microfat-stub-minimal -> store/current/microfat-stub-minimal
```

`owner.json` contains `schema: 1`, `kind: "microfat-installer"`, a random 32-hex `id`,
integer `uid`, and the exact `bin` and `store` roots. It contains no active-version authority.
`current` plus the selected generation is authoritative.

`generation.json` contains `schema: 1`, a random 32-hex `id`, canonical stable `version`
without `v`, `arch` (`amd64` or `arm64`), authenticated `archive_sha256`, and a `files`
object with exactly the three executable names. Each file has `size` and lowercase
`sha256`. Metadata is bounded to 32 KiB; unknown fields, duplicate members, unsupported
schemas, malformed identities and unexpected file types are rejected. These local records
retain verification facts; they are not signed provenance or overwrite authorization by themselves.

Published executable files and generation directories have mode 0755; metadata has mode 0644.
Private staging has mode 0700 and the stable lock file has mode 0600. Mutations require
current-user ownership, safe ancestors, regular single-link product/metadata files and no
other-user write permissions. Root-owned system generations are readable for discovery by
ordinary users. Same-UID malicious writers and root are outside this isolation boundary.

## Transaction and recovery

The helper snapshots roots, ownership and the selected generation before downloading.
After authenticating the frozen release, it takes a bounded exclusive advisory lock and
revalidates that snapshot. A competing activation invalidates the snapshot; the loser must
retry from a fresh inspection instead of silently overwriting the winner with an older download.
The `.lock` inode is retained across install and uninstall.

The installer stages all three verified products and metadata on the store filesystem,
closes write descriptors, syncs files and directories, publishes the complete generation,
and renames one temporary symlink over `current`. Existing entrypoint links stay unchanged.
An individual running CLI uses one physical generation; three separately started commands
are not a shared snapshot of the activation pointer.

On a first install, every existing entrypoint is checked before activation. Links are created
only after a complete generation is active, without replacing conflicting files. Interruption
can leave fewer entrypoints; every published link still resolves to a complete generation.
Rerunning the same authenticated installation completes missing links. Updates switch one pointer.

Failure before activation leaves the old selection intact. Failure after activation reports that
the new generation is active even if completion or durability confirmation fails. Interrupted
private staging is recovered under the lock. Complete generations remain available; no retention
count or `/proc` scan is treated as proof that a delayed process no longer needs its companions.
Process-crash tests do not establish physical power-loss guarantees on every filesystem.

Identical authenticated release bytes are reused only after metadata and product validation.
Corruption requires explicit `--repair`; unreadable active version metadata also requires
an explicit `--version`. Downgrade requires both `--version` and `--allow-downgrade`.

Manual installations and package-manager files are refused. There is no `--force-adopt` bypass.
Use empty custom roots or the reversible move-aside procedure in the installation guide.
Uninstall removes only entrypoint symlinks that still point exactly to this store. It preserves
modified or unrelated entries, all generations, the lock, ownership metadata and workload caches.

## Companion selection

The CLI resolves stubs in this order:

1. Explicit CLI `--stub` (relative to the working directory).
2. Manifest `stub` (relative to the manifest directory).
3. Physical sibling companion selected by CLI `--stub-profile`, manifest `stub_profile`, or `full`.
4. For unmanaged installations, executable companions in absolute `PATH` entries.

An explicit stub is caller-supplied input. It takes precedence, must be regular/readable, and
fails if invalid. Supplying a profile as well emits a notice that the custom path's profile
is not asserted. Explicit inputs need not be executable. Whitespace in paths is preserved.
Automatic candidates undergo bounded ELF machine inspection without execution. Empty/relative
`PATH` entries are ignored; a PATH match does not prove release provenance.

For a managed native CLI, Linux supplies the physical running image path. The launcher passes
that physical path through `MICROFAT_ORIGINAL_EXE` when dispatching via memfd or cache. Discovery
checks the running payload's size/digest against the original fat index, then validates the
physical generation, ownership, metadata and all three product hashes. It never follows a
moving public entrypoint to establish managed generation identity. A checksum alone is insufficient:
two releases can contain identical selected payload bytes.

A G1 process therefore selects G1 companions after G2 activation. Missing or inconsistent managed
metadata, files or payload hints fail managed discovery instead of falling through to G2 or an
unrelated PATH stub. Explicit stub overrides retain their caller-directed meaning. This is local
consistency validation, not publisher authentication or protection against a forged same-UID environment.
Older installed CLI versions retain their own discovery behavior; the stronger binding is implemented
in the v0.3.0 CLI, not retroactively added to immutable historical executables.

## Binary preservation

Installation copies authenticated executable bytes unchanged. Never run `strip`, `objcopy` or
`install -s` on a packed CLI: rewriting an ELF can remove its payload index and trailer.
Automatic generation garbage collection and package-manager adoption are not part of schema 1. Future consumers must preserve the stable lock, ownership checks, retained-reader
semantics and single activation pointer rather than replacing three files independently.

## Updater consumer

The v0.3.0 updater reads this schema without changing it. Read-only checks inspect both running and
active generations without creating a lock. Mutation requires the recorded owner, all matching
public links, valid product hashes, and a running generation equal to the active generation. Root
updates additionally require `--system`; roots are derived from validated metadata, never from an
updater destination override. Native execution is bound to the kernel's running inode; dispatched
execution retains the outer-image/payload checks above.

The updater captures its snapshot before release discovery/acquisition. Its existing-installation-only
transaction never recreates removed roots or initializes ownership. Under the stable lock it rechecks
root/owner identity, active generation metadata and hashes, and public links, including immediately
before activation. A changed installation requires a fresh invocation. An identical healthy target
is a no-op; explicit repair stays with the installer. Complete old generations remain available.

External distributions can place a nonempty regular, non-symlink `microfat-distribution.json` beside
the physical CLI. The schema is exactly `{"schema":1,"owner":"homebrew"}`; unknown fields, unsupported
values, other-user writable files and inputs over 4096 bytes are rejected. This marker selects the
fixed `brew upgrade microfat` guidance only. It cannot authorize installation writes, set a version,
choose a target path, or provide a command to execute. The official cask writes this marker with
mode 0644 beside all three unmodified executables in the versioned Caskroom. Native Homebrew
qualification checks its physical location and both memfd/cache discovery paths through brew's links.
The marker does not make the Caskroom an installer-owned generation and grants no retention or
transaction guarantees beyond Homebrew's own lifecycle. Unmarked external installations are still
refused by self-update. See [Homebrew maintenance](../homebrew.md).
