# Published signature fixture

These are the unmodified `checksums.txt` and `checksums.txt.sig` assets from
[microfat v0.2.4](https://github.com/EpicBlackWolfZ/microfat/releases/tag/v0.2.4).
They authenticate source `9607f3a0bb5954f37e8775351d76cee3398574a8` under
`https://github.com/EpicBlackWolfZ/microfat/.github/workflows/release.yml@refs/tags/v0.2.4`
and issuer `https://token.actions.githubusercontent.com`.

The Go contract test invokes an independently installed Cosign verifier against
these real signature bytes, including wrong identity/issuer/source/key and
modified/missing data. No release executable is contained here or run by that
test. The public certificate and transparency material are not credentials.

The installer tests separately verify archive checksum selection and rejection
before extraction/installation. The published-release audit tests actual archives.
