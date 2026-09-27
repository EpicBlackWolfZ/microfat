# Verified Linux installation

The v0.3.0 installer supports Linux amd64 and arm64. It installs the CLI and both launcher stubs
as one owned generation, using user directories by default. It does not require Go, preinstalled
Cosign, jq, Python or tar. Base requirements are Bash, curl with HTTPS support, trusted CA certificates,
and coreutils (`uname`, `mktemp`, `sha256sum`, `chmod`, `rm`, `stat`). Temporary staging must permit execution
and use an already existing parent with safe owned ancestors (root-owned sticky `/tmp` is supported).

**First-release availability:** the bootstrap is implemented but needs the signed v0.3.0 helper assets
to be published. A source checkout or unsigned snapshot alone does not make its public download path
usable. Until publication, a trusted source build can use the native helper with an independently
provisioned verifier as described below. The release workflow qualifies the signed draft before publication.

## Bootstrap and version selection

Obtain and review `scripts/install.sh` from a trusted, pinned source revision. Its embedded helper
version is v0.3.0, independently of the product version you request. Downloading and executing a
script over HTTPS trusts that source and transport. For a stronger operator workflow, download it
first, compare its digest with an independently trusted provisioning record, review it, then run it.
Do not treat a checksum fetched beside an untrusted script as an independent trust anchor.

After the first helper release is published, the versioned checkout-free command will be:

```bash
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
  https://raw.githubusercontent.com/EpicBlackWolfZ/microfat/v0.3.0/scripts/install.sh | bash -s --
```

This command is unavailable until v0.3.0 is published. Append installer arguments after `--`, such as
`--version 0.2.5`. To review first, download the same versioned URL with `curl --output microfat-install.sh`,
compare `sha256sum microfat-install.sh` with your independent trust record, inspect the script, and run
`bash microfat-install.sh`. From a reviewed checkout, after publication:

```bash
bash scripts/install.sh                         # discover and freeze latest stable
bash scripts/install.sh --version 0.2.5         # choose a supported stable version
bash scripts/install.sh --bin-dir "$HOME/tools/bin" --store-dir "$HOME/tools/microfat-store"
```

The bootstrap authenticates pinned Cosign v3.0.6 before executing it, verifies the helper's signed
checksums under the exact official v0.3.0 workflow identity, and checks the helper bytes. The helper
then authenticates the separately selected product archive before bounded extraction.
See the [complete trust policy](release-verification.md). Drafts and prereleases are never selected
by latest discovery; errors never cause fallback to a different release. Supported history starts
at v0.2.3. An older selection requires `--version VERSION --allow-downgrade`.

Defaults are `$HOME/.local/bin` and `${XDG_DATA_HOME:-$HOME/.local/share}/microfat/installations/default`.
Add the entrypoint directory to PATH yourself; the installer does not edit shell configuration.
For a noexec temporary filesystem, supply `--staging-dir /absolute/executable/directory`. The installer
does not change mount policy. System mode requires root plus explicit `--system`, `--bin-dir` and
`--store-dir`; it never invokes sudo or guesses which user's home should receive a root installation.
New installation directories use mode `0755`, including under a restrictive umask. Existing directories
keep their permissions; for system installs, choose existing ancestors that intended users can traverse.

## Trusted native helper

A trusted source build (`task build`) creates `bin/microfat-install`. A release helper is the separate
baseline static ELF asset `microfat-install_<version>_linux_<arch>`, not a fourth installed public product.
Authenticate that helper before first execution. It accepts an independently provisioned Cosign path
and digest. That executable and its ancestors must be owned by you or root and protected from
other users' writes; executable hardlinks and symlinks are refused. No arbitrary PATH verifier is trusted:

```bash
bin/microfat-install --version 0.2.5 \
  --cosign /opt/trusted-tools/cosign \
  --cosign-sha256 '<independent 64-digit lowercase SHA-256>'
```

`--helper-version` prints helper build identity; `--version` selects the product release.
For locally downloaded signed assets (including a private draft), add
`--release-dir /absolute/release-directory` and an explicit stable `--version`. The directory must
contain the exact archive, `checksums.txt` and `checksums.txt.sig`. Signature verification remains
mandatory. Local assets avoid product downloads; Cosign may still need trusted-root refresh/network
access. There is no unsigned installation or alternate publisher endpoint flag.

Exit codes are 0 for success/help, 2 for argument errors, and 1 for operation failures. A failure after
activation explicitly reports the newly active release. Reinstalling identical authenticated bytes
verifies and reuses the generation. `--repair` permits replacement of a corrupt owned generation;
if version metadata is unreadable, also pin `--version`. It never permits overwriting unmanaged files.

## Existing installations

The installer refuses manual files, modified public links and package-manager installations. Keep a
package-manager installation under its existing owner. To try the installer alongside it, choose
empty bin/store directories and invoke the new CLI by its absolute path.

For a manual installation you own, move all three executables to a separate backup directory before
installing into the now-empty bin directory. Record the paths first so you can restore them. Do not
move package-manager files behind its back. There is no automatic adoption or `--force` switch.
The old `scripts/install-release.sh VERSION ARCH [BIN_DIR]` entrypoint delegates to the bootstrap,
requires the native architecture, defaults to user storage, and accepts additional installer flags.
Its old arbitrary release URL, custom install runner and automatic sudo behavior are removed.

## Recovery and uninstall

Updates publish a complete generation before switching one `current` symlink. Existing binaries are
never rewritten. A first-install interruption may leave fewer public links, all pointing to complete
products; rerun the installer to complete them. Competing installation changes require a fresh retry.
The [ownership contract](design/installation-discovery-contract.md) describes the exact schema and
running-generation rules, including the boundary for older historical CLIs.

A trusted helper can uninstall without network access or Cosign:

```bash
bin/microfat-install --uninstall
# Repeat the same roots for a custom installation:
bin/microfat-install --uninstall --bin-dir "$HOME/tools/bin" --store-dir "$HOME/tools/microfat-store"
```

Uninstall removes only matching owned entrypoint links. It reports modified entries it preserved and
retains the generation store, lock and caches. Complete old generations remain because delayed running
commands may still need their companions. After all such processes have exited, and no installer or
uninstaller is running, you may deliberately remove that installation's retained store. Never remove
its lock during concurrent operations. No automatic pruning or background updater is included.

## Explicit release checks and updates

Starting with v0.3.0, use the CLI to check for and apply published stable releases:

```bash
microfat update --check
microfat update --check --json
microfat update
microfat update --version 0.3.0
microfat update --version 0.2.5 --allow-downgrade
```

Ordinary commands never check for updates or show update notices. `--check` reads official GitHub
HTTPS metadata and reports current/target versions. It does not download or execute a verifier,
create an installation lock, or change the installation. It reports `metadata_only`: availability
is not proof of signed artifact authenticity. The launcher's normal memfd/cache dispatch still
occurs before the command starts.

Successful checks return 0 whether or not a newer release exists. Use `--json` and `update_available`
for automation. An operational failure returns 1; invalid updater arguments return 2. Both checking
and applying accept `--json`. Stdout contains one result object; diagnostics go to stderr. Unknown
versions are null. `running_version` can differ from `current_version` when another process already
activated a newer generation. `verification` is `not_performed`, `metadata_only`, or `artifact_verified`.
`activated: true` means activation occurred even if a subsequent completion/durability/output error
caused a failure status. Do not assume a nonzero exit means the old release is still active.

Updates select latest stable by default, or a pinned published stable version from v0.2.3 onward.
Drafts and prereleases are rejected. A healthy identical version is a no-op. If your installation is
newer than latest, the default command leaves it alone. An explicitly older version requires both
`--version` and `--allow-downgrade`; checks may inspect an older version without that permission.
Downgrading below v0.3.0 removes the new update command. Use the verified installer to upgrade again.

Self-update requires a healthy installer-owned generation and all three matching public links.
The updater derives its destination from the validated running installation; there are no destination
or adoption flags. Manual and Homebrew installations with recognizable stable versions may check,
but must use their package manager or the migration procedure above to change versions. Homebrew
users should run `brew upgrade microfat`. Development builds with unknown versions cannot check.

Before applying an update, the CLI authenticates a pinned Cosign executable, signed checksums under
the exact official release workflow/tag and issuer, and the selected archive. Neither Go nor a PATH
Cosign is required. As with the bootstrap, trust in the running CLI and its embedded verifier pins
must already be established. You may supply `--cosign PATH --cosign-sha256 HEX` as an independent
verifier override. Acquisition uses private temporary storage; `--staging-dir /absolute/directory`
selects an existing safe parent. If verifier execution is denied, use an executable staging location
or an independently pinned executable verifier without changing mount/security policy.

Run system updates as the recorded root owner with `microfat update --system`. Checks require only
read access. The updater never invokes sudo and cannot take over a different user's installation.
A stale process refuses to update after another generation is activated: restart through the public
entrypoint. Competing updates require a fresh invocation rather than an automatic overwrite retry.

Failures before activation preserve the previous selection. Complete prior generations remain for
running processes. Corrupt installations require the installer's explicit repair procedure; the
updater does not adopt files, repair links, prune generations, or remove workload caches. Locally
downloaded signed drafts remain an installer qualification facility, not an updater release channel.
