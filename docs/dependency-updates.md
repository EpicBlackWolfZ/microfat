# Dependency updates

`go.mod` owns the executable Go version. CI reads its `go` directive with
`go-version-file`, and test fixtures derive the same installed toolchain rather
than carrying their own patch versions. References to the Go release family in
explanations or historical release evidence do not select a toolchain.

`.config/tool-versions.env` owns the developer, CI and release tool versions.
The local CI composite reads that file and exports action inputs. Shell
installers read the same pins. Downloaded tools use `version@SHA-256` tuples,
with one tuple per supported platform.

## Update ownership

| Dependencies | Updater | Integrity data |
| --- | --- | --- |
| Go modules in `go.mod` | Dependabot `gomod` | `go.sum`, including transitive modules |
| Workflow and composite action `uses:` references | Dependabot `github-actions` | Full upstream commit SHA and version comment |
| Fixture Docker base images | Dependabot `docker` | Image tag and manifest digest |
| Go patch in the root `go.mod` | Renovate custom manager | One shared Go directive, restricted to Go 1.27 |
| Task, GoReleaser, golangci-lint, govulncheck, actionlint, gotestsum and Codecov CLI | Renovate custom manager | Exact release or module version in the shared environment file |
| ShellCheck, cdxgen and Cosign downloads | Renovate release attachments | Exact release version and SHA-256 of each platform's asset |
| Homebrew qualification source | Renovate GitHub tags | Release tag and exact source commit in the shared environment file |
| Fortio benchmark tool | Renovate Go datasource | Module version, source commit and source URL in `benchmarks/tools.lock.json` |

Dependabot runs weekly and groups module, action and fixture image updates.
Go security updates have their own group. Renovate checks weekly and opens
reviewable PRs; automatic merging is disabled. Its enabled managers are limited
to custom managers so it does not duplicate Dependabot's module, action or image
PRs.

The hosted bots need repository access. Dependabot reads its configuration from
the default branch. The repository owner must enable the
[Renovate GitHub App](https://docs.renovatebot.com/getting-started/installing-onboarding/)
for this repository if it is not already selected. A configuration file or a
successful local dry run does not establish that the App is installed. The
hosted App supplies its own credentials; this setup adds no scheduled workflow
or repository token secret. Its Dependency Dashboard is the operational view
for lookup failures, held updates and open PRs after activation.

## Versions, asset checksums and source commits

For a tool with a native release or Go module version, place the datasource and
package name immediately before its assignment:

```sh
# renovate: datasource=github-releases depName=go-task/task
TASK_VERSION=3.54.0
```

The manager preserves the assignment's existing `v` prefix. A downloaded asset
instead needs a version and digest together:

```sh
# renovate: datasource=github-release-attachments depName=sigstore/cosign
COSIGN_LINUX_AMD64=v3.1.4@424e2c97d0b45fd7a6b608a87f090f35633f4c23ece5f092d48bba11aaf4bbfa
```

The [release attachments datasource](https://docs.renovatebot.com/modules/datasource/github-release-attachments/)
identifies the existing asset from its checksum, finds the corresponding asset
in the next release and computes its replacement digest. Each architecture is
matched separately; copies of the same package are updated in one PR. A
version-only edit with the previous release's hash is incomplete.

Homebrew uses the same tuple form with a 40-character commit instead of an
asset SHA-256 and a `github-tags` annotation. `HOMEBREW_SOURCE` is its canonical
source pin; qualification reads its version and commit together. Its manager
preserves the display `v` while looking up upstream's unprefixed Git tag.
Renovate resolves the new tag to its exact commit and updates both fields.

The public `scripts/install.sh` bootstrap is downloaded on its own, so its two
Cosign tuples are self-contained. They carry the same Renovate annotations as
the shared file. Both copies must advance together. The bootstrap helper's
product release pin is a separate release compatibility contract and is not a
third-party dependency update.

Fortio's Go datasource resolves its module version to the exact Git source
commit. Its manager replaces the lock's version, commit and source URL together.
The benchmark setup enforces the public Go checksum database when downloading
the exact module version, verifies the downloaded origin commit against the
lock, and retains the acquired module checksums in benchmark provenance. Static
copies of the Go `h1` checksums are unnecessary: the checksum database
authenticates module content, while the lock independently pins its source
identity. Benchmark result files retain their historical tool identities.

## Qualification fixtures and operating-system tools

`benchmarks/kernel.lock.json` deliberately selects a Linux 5.15 cgroup v1
qualification guest. It is a test compatibility boundary, so it does not track
the latest general-purpose kernel. Refresh its image and modules package names,
versions, URLs and SHA-256 values together from signed Ubuntu repository
metadata, then rerun the guest qualification. A Debian version-only updater
cannot regenerate this complete fixture safely.

QEMU, BusyBox, cpio and other runner packages are acquired through the runner's
authenticated APT repositories. They have no repository source-commit pin for
Renovate to change. The QEMU acquisition script records the selected package,
checks the signed repository's package SHA-256, and records the emulator's
version and binary checksum. Historical qualification commit IDs, release
digests and test fixtures remain evidence of those runs.

The embedded CycloneDX 1.7 and SPDX 3.0.1 schemas define supported document
formats and remain available offline. Their upstream revisions and byte hashes
are recorded in `internal/sbom/schema/README.md`. Refreshing those vendored
schemas requires replacing the complete schema/context bundle, checking its
recorded hashes and rerunning schema and release SBOM validation. Updating an
upstream tag or README pointer alone would not replace the embedded bytes;
these protocol contracts require a reviewed bundle update.

## Reviewing updates

Review the whole pin tuple and upstream release before merging. Run the normal
`task all` gate and the CI qualification jobs. Tool-download tests must exercise
the actual checksums, and a Fortio change must rebuild the tool and pass the
benchmark smoke test. Required hosted checks must pass at the final PR head.

Validate changes to `renovate.json5` with the
[Renovate config validator](https://docs.renovatebot.com/config-validation/).
Use a local extract or lookup dry run to check that every annotated tool, both
bootstrap tuples, Go and Fortio are found. The
[local platform](https://docs.renovatebot.com/modules/platform/local/) supports
read-only extraction and lookup; it does not create branches or prove that a
hosted bot has permission to open PRs. For checksum-manager changes, test an
older real version and its original asset hash in an isolated fixture so the
lookup must produce a new version and digest together.
