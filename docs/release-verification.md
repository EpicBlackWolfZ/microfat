# Verify a release before first execution

This is the publisher contract for the [verified installer](installation.md) and future distribution
integrations. `microfat verify` checks internal integrity after a program has started; it cannot
bootstrap trust in itself. See the [threat model](../SECURITY.md#8-threat-model-and-enforcement-map).

## Trust bootstrap

Trust begins with the host OS, Bash, HTTPS/CA configuration and checksum utilities, plus the reviewed
bootstrap source. An HTTPS script command trusts that source and transport. To pin it independently,
download/review the script first and compare it with a digest from a separate trusted provisioning
record. Do not accept a checksum beside an unauthenticated script as an independent anchor.

`scripts/install.sh` embeds Cosign v3.0.6 architecture pins from the reviewed
[sigstore/cosign-installer revision](https://github.com/sigstore/cosign-installer/blob/6f9f17788090df1f26f669e9d70d6ae9567deba6/action.yml):

| Architecture | SHA-256 |
| --- | --- |
| amd64 | `c956e5dfcac53d52bcf058360d579472f0c1d2d9b69f55209e256fe7783f4c74` |
| arm64 | `bedac92e8c3729864e13d4a17048007cfafa79d5deca993a43a90ffe018ef2b8` |

It checks the verifier before execution. It does not trust a PATH Cosign. An override requires both
`--cosign /absolute/regular/executable` and `--cosign-sha256 INDEPENDENT_DIGEST`; partial pins, symlinks,
relative paths, mismatching bytes and nonexecutable tools fail. Ambient `COSIGN_*` and `SIGSTORE_*`
options are removed before verification. Same-UID and privileged malicious writers remain outside
this boundary.

The bootstrap is fixed to helper release v0.3.0. It authenticates that release's checksum file with
Cosign, verifies `microfat-install_0.3.0_linux_<arch>`, and executes only the authenticated helper.
The helper version is independent of the requested product release. This avoids a circular dependency
when installing historical archives. The first public bootstrap is usable only after its helper release
is published; an unsigned snapshot proves packaging, not publisher authentication.

## Exact product release policy

1. Detect native Linux amd64/arm64. An explicit stable version is supported from v0.2.3 onward.
   Otherwise, bounded GitHub latest-stable discovery selects a version once. Discovery metadata is
   not authentication. Draft/prerelease responses are rejected, and failures never select another tag.
2. Authenticate the exact `checksums.txt` bytes using `checksums.txt.sig`, issuer
   `https://token.actions.githubusercontent.com`, and exact certificate identity
   `https://github.com/EpicBlackWolfZ/microfat/.github/workflows/release.yml@refs/tags/v<VERSION>`.
3. Retain Cosign certificate-chain and transparency verification. No alternate issuer/workflow,
   identity regex, arbitrary public key or insecure transparency bypass is accepted.
4. Strictly parse signed checksums, reject duplicates/unsafe names, require the selected exact
   `microfat_<VERSION>_linux_<arch>.tar.gz` entry, then check the archive digest before extraction.
5. Enforce archive path, type, count, size and duplicate-name bounds. Reject links and special files;
   verify required executable inventory, ELF architecture and fat CLI index without executing products.
6. Publish an owned generation only after authentication and validation. Downgrade of an existing
   managed installation requires both an exact `--version` and `--allow-downgrade`.

The helper bounds request time (two minutes), redirects (five), metadata (1 MiB), archive downloads
(256 MiB), extracted stream bytes (500 MiB), individual files (250 MiB) and archive entries (1024).
Product redirects must stay on the intended GitHub HTTPS hosts. Bootstrap downloads also require
HTTPS throughout, with bounded time, redirects and bytes; downloaded executables still require pins
or authenticated checksums before execution.

The installer does not add an independent source-commit constraint. Operators with a trusted source
pin can additionally use Cosign's `--certificate-github-workflow-sha`. The signed-draft and published
release qualification workflows do so. A tag API response alone is not an independent source anchor.

## Historical and locally staged releases

Published v0.2.3 uses the ordinary `release.yml@refs/tags/v0.2.3` identity. Do not broaden acceptance
to a historical repair workflow. v0.2.3/v0.2.4 retain CycloneDX 1.5/SPDX 2.3; v0.2.5 and later use the
modern schema policy. No immutable historical asset is rewritten. Versions below v0.2.3 have no
established installer signing contract and are refused.

A trusted helper accepts `--release-dir /absolute/assets --version VERSION` for locally downloaded
archive/checksum/bundle files, including an unpublished signed draft. It copies bounded regular files
into private staging and applies the same signature, digest and extraction policy. This avoids product
downloads, but does not promise offline Cosign root availability. Provision trusted Sigstore verification
material and test network-isolated verification separately before relying on it. Missing trust material
or unavailable root refresh remains an error; never bypass chain or transparency checks.

## What verification proves

The accepted signing identity authenticated these checksum bytes, and the chosen artifact matches its
signed digest. This does not independently prove reproducible source-to-binary builds, absence of
vulnerabilities, dependency-license completeness or an assessed SLSA level. Local ownership metadata
and running-payload checks establish consistency, not a new publisher-authentication mechanism.

[Real Cosign tests](../tests/e2e/release_signature_test.go) retain valid and wrong workflow, version,
issuer, source and key cases plus modified/missing evidence. [Acquisition tests](../internal/installrelease/)
cover bounded local/network input and authentication before extraction; [bootstrap tests](../cmd/microfat-install/bootstrap_test.go)
prove mismatched verifier/helper bytes never execute. Mocked control-flow tests are separate from actual
signature and native historical/signed-draft qualification.
