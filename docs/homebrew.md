# Homebrew release maintenance

Issue [#205](https://github.com/EpicBlackWolfZ/microfat/issues/205) implements the official Linux
cask in the shared [EpicBlackWolfZ/homebrew-tap](https://github.com/EpicBlackWolfZ/homebrew-tap)
repository. The Homebrew identifier is `EpicBlackWolfZ/tap`; future packages can coexist in the
same repository. Only `Casks/microfat.rb` belongs to microfat's automatic update path.

## First publication and ordering

The first cask targets v0.3.0. Merge the upstream tooling PR, review the exact upstream SHA in
the tap's `microfat-tools.ref`, and merge the tap bootstrap PR. Neither bootstrap publishes a cask.
Wait for the normal microfat release process to qualify, publish and finalize immutable v0.3.0
assets. Do not create a release, move a tag or alter historical assets to unblock tap work.

The tap's **Update microfat** workflow runs hourly at minute 17. A maintainer can also dispatch
it manually, optionally naming the exact latest eligible stable tag. Before v0.3.0 is public,
a successful check reports that no eligible release exists and creates no recipe. GitHub may
delay scheduled runs; manual dispatch is the retry path.

After publication, the workflow authenticates and prepares the recipe, runs native Homebrew
qualification on amd64 and arm64, then reauthenticates before opening a deterministic
`chore/microfat-v<version>` PR. A maintainer approves generated PR workflows, checks **Tap CI Complete**,
reviews the evidence and merges manually. Verify the documented install command from the public
tap after merge. Only then update availability documentation and close #205 as complete.

The ordinary tag release workflow has no tap credentials or tap write step. A tap failure leaves
the already-published upstream release alone and keeps the previous tap version available.

## Authentication and recipe generation

`internal/homebrew` and `internal/cmd/homebrew-release` are maintained and tested with the upstream
release contract. The tap pins their source by full commit SHA, separately from the selected
product release. Dependency/action/tool updates require review. Qualification pins Homebrew 7.0.2
at `83c9802fb54c60a612d52b264808c099b39a0687`.

The production generator accepts only exact stable public immutable releases from
`EpicBlackWolfZ/microfat`, starting at v0.3.0. It freezes the release ID, tag and source commit,
downloads the complete expected inventory into new private staging, and authenticates an
independently SHA-pinned Cosign v3.0.6. Signature verification uses the exact official release
workflow/tag identity, GitHub issuer and source SHA. See [release verification](release-verification.md).

All twelve payload hashes must match the signed checksum document. Both architecture archives,
both native helpers and their linked SPDX/CycloneDX documents must satisfy the existing release
contract. Authentication precedes extraction; no downloaded product executes during generation.
The renderer uses only verified archive hashes and canonical versioned GitHub URLs. A failed
authentication or artifact check produces no candidate cask. There is no alternate publisher,
unsigned mode, user-supplied checksum or production download-URL override.

The cask uses declarative `binary` links and constrained `preflight_steps` to write the fixed
Homebrew ownership marker. It preserves the three executable products unchanged; no source build,
strip, patchelf, custom installer or generic shell hook runs. Top-level Linux and architecture
requirements exclude macOS and unsupported CPUs. See the [Homebrew cask cookbook](https://docs.brew.sh/Cask-Cookbook).

## Credentials, idempotence and review

Only the tap's final proposal job receives `contents: write` and `pull-requests: write` on its own
GITHUB_TOKEN. Earlier jobs and all PR validation have read-only permissions; checkout credentials
are not persisted. Enable GitHub Actions PR creation in the tap repository settings. No PAT,
GitHub App or upstream cross-repository secret is required. Protect `main` with the **Tap CI Complete**
check and require pull requests; maintainers merge manually.

GitHub requires a maintainer to approve workflows on PRs created with GITHUB_TOKEN. Do not replace
that approval step with a privileged `pull_request_target` workflow or automatic approval. See
[GitHub's token behavior](https://docs.github.com/en/actions/concepts/security/github_token).

An identical current recipe is a no-op. A newer release creates or reuses its deterministic branch
and PR. Reuse requires identical authenticated cask bytes and no unrelated changed paths.
Downgrades, changed same-version recipes, human edits, closed PRs and unexpected branch content
fail for manual review. The workflow never force-pushes, commits to `main`, merges, or changes
another package. Same-version repairs require a separately reviewed policy/tooling change, not
an automatic overwrite. Concurrency serializes updater runs; latest and current-main checks before
proposal prevent ordinary stale candidates from replacing a newer cask.

## Local commands and evidence

Use Go 1.27.2 and Task v3.53.1 from a reviewed checkout:

```bash
task homebrew-check
task homebrew-prepare TAG=v0.3.0 OUTPUT=/absolute/new-candidate-directory
task homebrew-check CASK=/absolute/new-candidate-directory/microfat.rb
task qualify-homebrew OUTPUT=/absolute/new-qualification-directory \
  CASK=/absolute/new-candidate-directory/microfat.rb
```

Preparation requires a real eligible release and network access. `homebrew-check` without `CASK`
runs the deterministic generator tests. With `CASK`, it downloads and authenticates the release
again and requires exact regenerated bytes. Output directories must be new; create their parent
directories first. Failed staging is retained for diagnosis. GH_TOKEN is optional for public API
rate limits and is sent only to api.github.com.

`qualify-homebrew` creates an isolated Homebrew prefix, home, logs and caches without touching the
operator's installed Homebrew. It requires a non-root native Linux user, Git, CA certificates,
curl and the normal Homebrew Linux prerequisites. It installs local controlled archives to test
reinstall, successful upgrade and a corrupted-checksum failure. It checks all three product hashes,
link layout, marker permissions, PATH use, platform metadata, both stub profiles, memfd/cache
execution, update checks/refusal and uninstall preserving unrelated files and workload caches.
With `CASK`, it also checks style/audit and installs that exact authenticated public recipe.

Upstream CI feeds its GoReleaser snapshot into `DIST` on native amd64/arm64 runners. Snapshots test
the real archive layout and byte preservation but are not publisher-authentication evidence.
Controlled fixture versions likewise never become official recipes. Tap PR checks run the native
matrix again; absence of a cask is permitted only while bootstrapping the first publication.

Retained evidence includes command logs, native architecture, pinned Homebrew revision, tooling
source pin, release ID/tag/source SHA, signed checksum and asset hashes, installed product hashes,
recipe digest and Go/Cosign versions. Use the workflow run linked from a generated PR to inspect
both native jobs. Failures must be diagnosed before rerunning; never substitute placeholder hashes
or skip authentication to publish a recipe.
