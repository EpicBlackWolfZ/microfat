# Verify a release before first execution

This is the shared publisher contract for installation and future distribution/updater integrations.
It authenticates an archive before any downloaded microfat program executes. `microfat verify`
checks internal integrity after a program has already started; it cannot bootstrap trust in itself.
See the [threat model](../SECURITY.md#8-threat-model-and-enforcement-map).

## Trust bootstrap

Start with a trusted OS, shell, HTTPS/download/checksum utilities and an independently installed
Cosign verifier. Acquire Cosign through your trusted package manager or verify the pinned upstream
release by an independent trust path using [Sigstore's installation instructions](https://docs.sigstore.dev/cosign/system_config/installation/).
Our CI pins Cosign v3.0.6 through a reviewed immutable installer-action revision that embeds its
download digests. Trusting that action/repository is an explicit CI bootstrap assumption.

The source installer uses the host's trusted `PATH` by default. A fake `cosign` earlier in `PATH`
can claim any signature is valid; microfat cannot detect a compromised host toolchain by asking it
to verify itself. For an independently provisioned verifier, pin both its absolute path and digest:

```bash
export MICROFAT_COSIGN=/opt/trusted-tools/cosign
export MICROFAT_COSIGN_SHA256='<64 lowercase hex digits from your trusted provisioning record>'
bash scripts/install-release.sh 0.2.5 amd64 "$HOME/.local/bin"
```

This digest must come from an independent trusted record, not be calculated from an untrusted
download and immediately accepted. The installer rejects partial pins, relative paths, symlinks,
missing/nonexecutable tools and mismatching bytes **before invoking the verifier**. The installer
source and its digest must themselves be trusted; fetching and running an unreviewed script is
also execution before authentication. Same-UID/privileged writers remain outside this boundary.

## Exact release policy

For release tag `v<VERSION>`, require all of the following:

1. An explicitly chosen version and architecture. Use `microfat_<VERSION>_linux_<amd64|arm64>.tar.gz`;
   never substitute a different tag, follow `latest`, or silently fall back to another release.
2. `checksums.txt.sig` must authenticate the exact downloaded `checksums.txt` bytes under issuer
   `https://token.actions.githubusercontent.com` and **exact** certificate identity
   `https://github.com/EpicBlackWolfZ/microfat/.github/workflows/release.yml@refs/tags/v<VERSION>`.
   Do not use an identity regex, alternate workflow, arbitrary public key or user-supplied issuer.
3. Verify Cosign's certificate chain and transparency evidence using trusted Sigstore verification
   material. Do not disable transparency checks or treat an unavailable verifier/network as success.
4. Select exactly one checksum entry whose complete filename matches the requested archive. Require
   a 64-digit SHA-256 value and match the downloaded archive **before extraction or installation**.
5. Extract into a fresh unprivileged directory, check the required products, and install only the
   three executables. Do not execute any downloaded product as part of authentication.

The [installation example](release-artifacts.md#scenario-a-installing-the-microfat-cli) and
[source installer](../scripts/install-release.sh) implement these identity/digest rules. The installer
does not add a source-commit constraint. Operators who independently pin the tag's source commit
can additionally use `--certificate-github-workflow-sha <40-digit-source-sha>` as the
[published-release audit](../.github/workflows/release-audit.yml) does. A tag API response alone is
not an independent source trust anchor.

Version pinning prevents accidental substitution; it is not automatic downgrade protection.
An operator selecting an old signed version explicitly accepts its age and known defects. Updaters
must retain an approved version/floor independently and reject rollback according to their policy.
This release does not add an updater or an embedded authentication format.

## Historical releases and offline verification

Published v0.2.3 uses the ordinary `release.yml@refs/tags/v0.2.3` identity, as explained in its
[corrected release notes](releases/v0.2.3.md#published-signature-and-sbom-verification).
Do not broaden acceptance to `release-sbom-repair.yml` because an old draft once used a repair
workflow. No historic immutable asset is changed by this policy. For earlier releases, consult
the exact release's signing record and reject unestablished identities instead of guessing.
Historical CycloneDX 1.5/SPDX 2.3 documents remain distinct from the new release schema policy.

The installer downloads over HTTPS and is an online installation workflow. For offline verification,
stage the archive, signed checksum bytes, signature bundle, independently authenticated verifier,
and trusted Sigstore root material while connected. A supported Cosign version can verify bundle
evidence with `--trusted-root /trusted/path/trusted_root.json`; the root is a trust input, not an
asset to accept from an unauthenticated release download. See
[Sigstore verification](https://docs.sigstore.dev/cosign/verifying/verify/) and
[trusted-root configuration](https://docs.sigstore.dev/cosign/system_config/custom_components/).
Test the chosen version/root/bundle combination without network access before depending on it.
Missing evidence, unsupported old bundle formats or root-refresh requirements are failures;
never work around them with `--insecure-ignore-tlog`, `--insecure-ignore-sct` or custom untrusted roots.

## What successful verification proves

The accepted signing identity authenticated these checksum bytes, and the selected archive matches
the signed digest. A separately supplied source-SHA constraint binds the certificate to that
workflow revision. This does not independently prove source-to-binary reproducibility, a complete
build provenance chain, absence of vulnerabilities, correctness of dependency attribution or any
assessed SLSA level. SBOMs and hosted benchmark evidence provide additional, separately validated
information. Format v2 is unchanged.

[Real Cosign contract tests](../tests/e2e/release_signature_test.go) use the published v0.2.4
signature for valid and wrong workflow/version/issuer/source/key cases and modified/missing
signature/checksum data. [Installer tests](../tests/e2e/install_script_test.go) reject missing or
modified archives and ambiguous/malformed checksums before installation;
[verifier-pin tests](../tests/e2e/install_verifier_test.go) prove that untrusted verifier bytes
are not executed. Mock installer tests validate control flow, while the real signature test and
published-release audit supply cryptographic and downloaded-product evidence respectively.
