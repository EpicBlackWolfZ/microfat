# Contributing to microfat

Thank you for your interest in contributing to `microfat`! We welcome contributions, bug reports, feature suggestions, and documentation improvements from the open-source community.

---

## 1. Code of Conduct

All participants in the `microfat` community are expected to adhere to the [Code of Conduct](CODE_OF_CONDUCT.md). Please review it before contributing.

---

## 2. Community Channels

- **Discussions & Ideas**: Use [GitHub Discussions](https://github.com/EpicBlackWolfZ/microfat/discussions) for general questions, architecture ideas, and RFC proposals.
- **Bug Reports & Feature Requests**: Use [GitHub Issues](https://github.com/EpicBlackWolfZ/microfat/issues/new/choose) and select the appropriate issue form.

---

## 3. Development Prerequisites & Setup

- **Go Toolchain**: Go **1.27.1**, matching `go.mod` and CI.
- **Task**: [Task](https://taskfile.dev/) **v3.53.1**, pinned in CI.
- **Git**: Working copy cloned from `https://github.com/EpicBlackWolfZ/microfat`.
- **Required for `task all`**:
  - `golangci-lint` (v2.13.2+)
  - `shellcheck` (v0.11.0)
  - `govulncheck`
- **Additional tools**: `gotestsum` is optional for test presentation; `goreleaser` is required for `task snapshot`.
- **Release SBOM tests and packaging**: install pinned standalone cdxgen `cdx-convert` **v13.1.0**
  with `bash scripts/install-cdxgen.sh "$HOME/.local/bin"` and add that directory to `PATH`.
  CI and the release gate set `MICROFAT_SBOM_TESTS=required`, which fails when the tool is missing.
  Run `MICROFAT_SBOM_TESTS=required task test` to require the real AMD64/ARM64 SBOM tests locally.
  These tests build variants with differing dependency versions and local replacements.

Missing mandatory linters or scanners fail the task; no weaker fallback is substituted.
### Quickstart

```bash
# Clone the repository
git clone https://github.com/EpicBlackWolfZ/microfat.git
cd microfat

# Install the exact Task version using Go module checksum verification
go install github.com/go-task/task/v3/cmd/task@v3.53.1
export PATH="$(go env GOPATH)/bin:$PATH"

# Verify dependencies, run full linting, vulnerability scans, and test suite
task all
```

---

## 4. Task Developer Commands

All standard developer operations are automated through the root `Taskfile.yml`. Run `task --list`
for the complete list; `task` defaults to `task all`.

```bash
task help       # View all available targets and descriptions
task all        # Run tidy, lint, vuln, test, coverage gate (>=95%), and build
task build      # Compile microfat and microfat-stub into bin/
task test       # Run unit tests with race detection (-race)
task coverage   # Generate coverage profile and enforce >= 95.0% threshold gate
task coverage-unit # Enforce the same statement gate before black-box integration tests
task lint       # Run all linters (Go via golangci-lint and Bash via shellcheck)
task lint-go    # Run golangci-lint across all packages
task lint-shell # Run ShellCheck (v0.11.0) across all tracked Bash scripts
task vuln       # Run govulncheck vulnerability scanner
task tidy       # Run go mod tidy and go mod verify
task snapshot   # Test GoReleaser release packaging and self-bundling
task demo       # Build demo fat binary in examples/demo
task bench      # Run benchmark suite in examples/demo
task clean      # Remove build artifacts and coverage files
```

Task preserves the existing command names and parameters. Set values in the environment or after
the task name; command-line values take precedence, including in nested tasks:

```bash
task build BIN_DIR='.work/custom bin' VERSION=local-check
task pprof PROFILE=block PORT=6061 HTTP_PORT=8081
NO_COLOR=1 task help
```

Other supported overrides include `GO`, `GOFMT`, `HOST_ARCH`, `LDFLAGS`, `COMMIT`, `DATE`,
`COVERAGE_FILE`, `COVERAGE_THRESHOLD`, `FUZZTIME`, `DURATION`, `TRIALS` and `BENCHMARK_CONFIG`.
The demo has its own `Taskfile.yml` (`task --dir examples/demo --list`) and supports `BINARY_NAME`,
`BIN_DIR`, `MICROFAT`, `MICROFAT_STUB`, `REPO_ROOT_BIN` and `INSTALL_DIR`. Root/demo Makefiles and
Make compatibility wrappers are removed. Formatting and tidy checks remain read-only; only
`fmt`, `fix` and `tidy` modify sources or module metadata.

Verification and shared-output producers run sequentially inside the aggregate tasks. Run snapshots
one at a time per checkout, since GoReleaser owns `dist/`. `NO_COLOR=1`, `COLOR=0` or `COLOR=false`
disable optional colors; redirected output stays plain. Profiling stops on occupied or unknown ports
and cleans up only its own child processes.

`task test` runs all default and minimal package tests with the race detector. `task coverage` uses
the same profile definitions and includes `cmd`, `internal`, `runtimeinit` and `benchmarks` code.
Example and E2E packages run in both profiles; their source is not included as production statements.
Default/minimal profiles remain under ignored `.work/tests/`; `coverage.out` is their source-block
union. Shared blocks count once, exclusive files count once, incompatible instrumentation fails,
and the >=95% overall gate counts statements rather than averaging package percentages. JUnit
artifacts use separate profile names. Temporary planning documents and verification evidence belong
under ignored `.work/`, not published documentation directories.

CI uses `task coverage-unit` for its middle stage. It runs the unit tests in `cmd`, `internal`,
`runtimeinit` and `benchmarks` with both profiles and the same production statement universe.
The dedicated black-box suite runs afterwards through `task e2e`, also with both profiles.
The full local and release commands retain their existing complete test and coverage scope.

Workflow actions are pinned to upstream commit SHAs with version comments; Dependabot maintains
these pins. Publishing/OIDC permissions belong only to the release publisher. Fork PRs retain read-only
tokens, skip checks-write reporting, and still enforce test failures through the test job. The release
verification job does not inherit publishing permissions. No action-pinning exceptions are intended.

---

## 5. Conventional Commits & Pull Request Format

We strictly follow the [Conventional Commits](https://www.conventionalcommits.org/) specification for Pull Request titles.

> [!IMPORTANT]
> `microfat` enforces **Squash Merging only**. The Pull Request title becomes the final squash commit headline on `main`. Ensure your PR title is descriptive and follows the conventional commit syntax.

### PR Title Syntax:
```
<type>(<optional-scope>): <description>
```

### Allowed Types:
- `feat`: A new feature, microarchitecture level, or CLI capability
- `fix`: A bug fix
- `docs`: Documentation updates or additions
- `perf`: Performance improvements
- `refactor`: Code refactoring without behavior changes
- `test`: Adding or enhancing test suites
- `build`: Changes to build system, toolchain, or dependencies
- `ci`: Changes to CI/CD workflows or pipeline scripts
- `chore`: Housekeeping, repository maintenance, or dependency bumps
- `revert`: Reverting a previous commit

---

## 6. Automated Quality & Security Gates

The `main` ruleset requires **`CI Complete`** together with the existing six checks below.
`CI Complete` validates every expected stage result; missing jobs, failure, cancellation and
unexpected skips fail the gate. A matrix must succeed as a whole. The only accepted skips are
the code stages of a positively classified documentation-only PR, as described below.

1. **`Validate Conventional PR Title`**: Verifies PR title follows Conventional Commits.
2. **`Lint (golangci-lint)`**: Zero tolerance for Go linter errors (`errcheck`, `goconst`, `lll`, `mnd`, `govet`, etc.) and ShellCheck (v0.11.0) static analysis violations across all tracked Bash scripts.
3. **`Unit Tests & Coverage Gate (>= 95%)`**: Default/minimal unit tests with `-race`, enforcing the exact **>= 95%** production statement union across `cmd`, `internal`, `runtimeinit` and `benchmarks`.
4. **`Vulnerability Scan (govulncheck)`**: Automated scanning against the official Go Vulnerability Database.
5. **`Secrets Detection (gitleaks)`**: Audits commits for accidental credential leaks.
6. **`Build, GoReleaser Snapshot & Self-Bundling Verification`**: Verifies multi-architecture builds and self-dispatching stubs.

All PR work shares one dependency graph. Formatting, module/configuration/workflow checks,
Go/ShellCheck lint, title validation, secrets and vulnerability checks run first. Only after they
succeed can unit/race/coverage run. Integration/chaos/fuzz, developer regressions and leak checks,
serialized release snapshots, benchmark smoke, archive contracts, all three kernel modes and
CodeQL wait for the unit gate. `CI Complete` explicitly requires every stage, including the
reusable workflow results. CodeFactor remains a separate quality signal and release acceptance
check; it is not a named ruleset requirement.

Main pushes use the same staged validation. Scheduled/manual deep benchmarks, calibration,
release evidence and publication retain their own workflows. Reusable jobs have distinct
concurrency groups so they cannot cancel their parent CI run. Snapshots sharing `dist/` remain
serialized within one job.

Maintainers can use the CI workflow's manual `failure_probe` input to verify propagation of a
fast, unit, final-stage or matrix failure, or an unexpected skip. The `cancel` probe holds a
bounded cancellation window before unit tests; cancelling that run must not produce a green
aggregate. These probes deliberately fail validation. Use `none` for ordinary qualification.
When changing required checks, first prove the new context on the candidate PR, then add it to
the ruleset while retaining existing protections before merging the workflow change.

---

## 7. Branch Protection & Contribution Flow

> [!NOTE]
> Direct pushes to `main` and tag overwrites on `v*` are blocked by Repository Rulesets. All contributions must be submitted through Pull Requests.

### Step-by-Step Contribution Flow:

1. **Fork the Repository**: Create a fork of `EpicBlackWolfZ/microfat` on GitHub.
2. **Create a Topic Branch**:
   ```bash
   git checkout -b feat/my-awesome-feature
   ```
3. **Develop & Test Locally**: Implement your changes and verify with `task all`:
   ```bash
   task all
   ```
4. **Push & Open a Pull Request**:
   - Push your branch to your fork.
   - Open a PR against `main`.
   - Fill out the PR description template.
5. **Keep Branches Up-to-Date**:
   - If `main` advances, use the 1-click **Update branch** button in the GitHub PR interface.
6. **Code Review & Resolution**:
   - Address any reviewer comments. All conversation threads must be resolved before merging.
7. **Merge**:
   - Once required checks are satisfied and reviews are complete, the maintainer can merge the PR.

## 8. Documentation-only pull requests

The required CI workflow runs on every pull request to `main`. PR title validation, secret scanning
and change-classification regression checks also run for documentation changes. A nonempty diff
limited to root Markdown files, Markdown under `docs/`, and `LICENSE` skips Go lint, tests, developer
runtime checks, vulnerability analysis, benchmarks, CodeQL and snapshots at the step/job level, so required statuses
finish instead of remaining pending. Other paths, mixed changes and empty diffs run code checks.

Classification uses the complete Git diff of the PR merge result against its first parent, with
rename detection disabled. A Git or classifier failure fails the required lint job; it cannot approve
a documentation-only skip. The final aggregate checks the explicit skip set while title, lint
classification and secrets must succeed. Push and ordinary manual runs retain the full CI checks.
Run `bash scripts/ci-code-changes-test.sh` to verify the policy and `go test ./internal/cigate`
to check the workflow dependency and completion contracts.

To inspect a staged run, open the PR's **CI Complete** check and follow its prerequisites in
GitHub Actions. A later job marked **skipped** after an earlier failure has not passed validation;
fix the first failing prerequisite and run CI again. For a docs-only PR, code-stage skips are
expected only when classification, title validation, secret scanning and **CI Complete** all pass.
