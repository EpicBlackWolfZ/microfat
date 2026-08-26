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

- **Go Toolchain**: Go **1.27** or newer.
- **Git**: Working copy cloned from `https://github.com/EpicBlackWolfZ/microfat`.
- **Optional Local Linters / Tools**:
  - `golangci-lint` (v2.13.1+)
  - `gotestsum`
  - `goreleaser` (v2+)
  - `govulncheck`

### Quickstart

```bash
# Clone the repository
git clone https://github.com/EpicBlackWolfZ/microfat.git
cd microfat

# Verify dependencies, run full linting, vulnerability scans, and test suite
make all
```

---

## 4. Makefile Developer Targets

All standard developer operations are automated via `make`:

```bash
make help       # View all available targets and descriptions
make all        # Run tidy, lint, vuln, test, coverage gate (>=95%), and build
make build      # Compile microfat and microfat-stub into bin/
make test       # Run unit tests with race detection (-race)
make coverage   # Generate coverage profile and enforce >= 95.0% threshold gate
make lint       # Run golangci-lint across all packages
make vuln       # Run govulncheck vulnerability scanner
make tidy       # Run go mod tidy and go mod verify
make snapshot   # Test GoReleaser release packaging and self-bundling
make demo       # Build demo fat binary in examples/demo
make bench      # Run benchmark suite in examples/demo
make clean      # Remove build artifacts and coverage files
```

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

Every Pull Request must pass **7 required automated CI and Security checks**:

1. **`Validate Conventional PR Title`**: Verifies PR title follows Conventional Commits.
2. **`Lint (golangci-lint)`**: Zero tolerance for linter errors (`errcheck`, `goconst`, `lll`, `mnd`, `govet`, etc.).
3. **`Unit Tests & Coverage Gate (>= 95%)`**: Full test execution with `-race` detection enforcing the strict **>= 95.0%** threshold across all packages (`cmd/...`, `internal/...`, and `runtimeinit/...`).
4. **`Vulnerability Scan (govulncheck)`**: Automated scanning against the official Go Vulnerability Database.
5. **`Secrets Detection (gitleaks)`**: Audits commits for accidental credential leaks.
6. **`Build, GoReleaser Snapshot & Self-Bundling Verification`**: Verifies multi-architecture builds and self-dispatching stubs.
7. **`CodeQL Advanced (Go & Actions)`**: Deep semantic AST static security analysis.

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
3. **Develop & Test Locally**: Implement your changes and verify with `make all`:
   ```bash
   make all
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
   - Once all 7 status checks turn green and reviews are complete, the PR is squash-merged to `main`, and the feature branch is automatically deleted.
