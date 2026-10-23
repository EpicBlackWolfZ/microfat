# Held-out application corpus

This shared research recipe builds four application families that were absent from
the demo/microfat training set: Fortio, Task, actionlint and govulncheck. Versions
come from the repository's existing tool pins. `sources.lock.json` records Go module
and go.mod checksums plus source commits; the builder verifies all five identity
fields against Go's downloaded module metadata before compiling.

Run the root `research-heldout-corpus` Task target with a new `CORPUS_OUTPUT` and
native `CORPUS_ARCH=amd64|arm64`. It requires Go 1.27.3, jq and sha256sum. Building
is outside timed windows. The script never executes a downloaded application.
Consumers may execute only the explicitly allowlisted version command, with their
own process/output/time limits. These commands measure process completion, not
network-service readiness.

Schema 2 records absolute paths, SHA-256, byte counts, architecture, source hashes,
build flags and the microfat checkout revision used for the stub and recipe. It
contains stripped/unstripped builds at three ISA levels per family. Profiles and
ISA levels are repeated observations of a family, not independent held-out apps.
Binary buildinfo and module metadata remain beside the manifest. Outputs and
partial failures are retained and an existing output directory is refused.

AMD64 levels are v1/v2/v3; ARM64 levels are v8.0/v8.1/v8.2. Building an ARM64 corpus
requires a native ARM64 Go host. No cross-compiled or emulated timing is reported.
Module-cache file paths are local diagnostics, not portable source identities.
The manifest's source revision identifies the corpus producer; experiment code
and timing reports retain their own separate source revisions.
