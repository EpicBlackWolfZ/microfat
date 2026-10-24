# Codec policy qualification corpus

The fixed Go application families are Fortio, Task, actionlint and govulncheck.
`sources.lock.json` is copied unchanged from the shared held-out corpus recipe on
`origin/perf/research-47-sharing-screen` at
`7ff78252d68eaee073dcbdae4ac4a04dec865dda`. The builder verifies module path,
version, module checksum, go.mod checksum and source commit against downloaded Go
module metadata before compiling. These pinned families are not retuned after
qualification results are collected.

Run `bash scripts/codec-policy-corpus.sh NEW_OUTPUT [amd64|arm64]` with Go 1.27.3,
jq, sha256sum and GNU timeout. Task's `GO` executable override is selected before
the inherited Go environment is cleared. The architecture defaults to the native Go host. A native Linux
host is required; cross-compilation and emulation are excluded from timing.
Building happens outside measurement windows. The script never executes a
downloaded application. It removes inherited `GO*`, `CGO*`, `MICROFAT*` and
`SOURCE_DATE_EPOCH` settings, disables Go's persistent environment and workspaces,
uses the local toolchain, and fixes `GOMAXPROCS=4` and `CGO_ENABLED=0`.

Each Go family has stripped and unstripped builds at three ISA levels: AMD64
v1/v2/v3 or ARM64 v8.0/v8.1/v8.2. Profiles and levels are repeated observations
within one family, not independent applications. Baseline full and minimal
stubs and the microfat CLI are built from the current checkout with the same
explicit build settings. Each compiler/download command has a 600-second timeout
and a 10-second termination grace period. Module metadata, binary buildinfo, host and toolchain
identity, source status, variant hashes and byte counts remain beside the schema
2 manifest. `SHA256SUMS` covers all generated files. Existing output directories
are refused and partial outputs are retained. The source must be clean and
committed; it is checked again after building and a change rejects the corpus.
Use an external or ignored output path, such as `.work/codec-policy-corpus`.
Module downloads run outside the checkout and builds use `-mod=readonly`.
The manifest's `toolchain.path` and `toolchain.sha256` identify the selected Go
command file, including a wrapper when one is selected. `go-env.json` records the
underlying GOROOT; collection separately freezes its Go, compiler and linker
executables.

The synthetic `boundary` family consists of valid static Linux ELF executables
with one executable `PT_LOAD` segment and no dynamic loader or Go runtime. Its
three profiles have file sizes exactly 524287, 524288 and 524289 bytes. A small
architecture-specific syscall program writes `microfat-codec-boundary` followed
by a newline, then exits successfully; deterministic pseudorandom padding fills
the remainder. The generator lives at
`tests/e2e/testdata/codecboundary/main.go`. All three ISA entries in each boundary
profile point to the same baseline-compatible file, so they cannot be treated as
independent payloads or application performance evidence. These fixtures test
the size boundary without attributing their behavior to Go applications.
The manifest keeps the four pinned Go sources in `sources` and records the
boundary generator and its checksum separately in `synthetic_sources`.

The only allowed commands for the qualification consumer are:

| Family | Arguments | Completion evidence |
| --- | --- | --- |
| Fortio | `version` | Exit status and captured version output |
| Task | `--version` | Exit status and captured version output |
| actionlint | `-version` | Exit status and captured version output |
| govulncheck | `-version` | Exit status and captured version output |
| boundary | none | Exit status and the fixed marker |

Consumers impose their own process, output and timeout limits. The version
commands measure process completion, not network-service readiness. The manifest
source revision identifies the corpus producer; experiment code and reports
record their own revisions separately. Module-cache paths are local diagnostics,
not portable source identities.
