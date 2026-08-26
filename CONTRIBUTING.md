# Contributing to microfat

Thank you for your interest in contributing to `microfat`! We welcome contributions, bug reports, feature suggestions, and documentation improvements.

---

## 1. Code of Conduct

All contributors and maintainers are expected to adhere to the [Code of Conduct](CODE_OF_CONDUCT.md). Please report unacceptable behavior following the guidelines in that document.

---

## 2. Development Setup & Prerequisites

- **Go Toolchain**: Go **1.27** or newer.
- **Git**: Working copy cloned from `https://github.com/EpicBlackWolfZ/microfat`.
- **Optional Tooling**:
  - `golangci-lint` (v1.64+)
  - `gotestsum`
  - `goreleaser` (v2+)

### Quickstart

```bash
# Clone the repository
git clone https://github.com/EpicBlackWolfZ/microfat.git
cd microfat

# Verify dependencies and run complete test suite
make all
```

---

## 3. Makefile Targets

All standard developer workflows are automated via `make`:

```bash
make help       # View all available targets and descriptions
make all        # Run tidy, lint, vuln, test, coverage gate (>=95%), and build
make build      # Compile microfat and microfat-stub into bin/
make test       # Run unit tests with race detection (-race)
make coverage   # Generate coverage profile and enforce >= 95.0% threshold gate
make lint       # Run golangci-lint across all packages
make vuln       # Run govulncheck vulnerability scanner
make tidy       # Run go mod tidy and go mod verify
make snapshot   # Test GoReleaser release packaging without publishing
make demo       # Build demo fat binary in examples/demo
make bench      # Run benchmark suite in examples/demo
make clean      # Remove build artifacts and coverage files
```

---

## 4. Conventional Commits & Pull Request Guidelines

We strictly follow the [Conventional Commits](https://www.conventionalcommits.org/) specification for commit messages and Pull Request titles.

### PR Title Format:
```
<type>(<optional-scope>): <description>
```

### Allowed Types:
- `feat`: A new feature or capability
- `fix`: A bug fix
- `docs`: Documentation updates or additions
- `perf`: Performance improvements
- `refactor`: Code refactoring without behavior changes
- `test`: Adding or enhancing test suites
- `build`: Changes to build system, toolchain, or dependencies
- `ci`: Changes to CI/CD workflows or pipeline scripts
- `chore`: Housekeeping, repository maintenance

---

## 5. Quality Standards & Testing

1. **Test Coverage Gate**: Every Pull Request must satisfy the strict **>= 95.0%** code coverage gate across core packages (`cmd/...` and `internal/...`).
2. **Race Detection**: All tests must pass with data race detection enabled (`go test -race`).
3. **Zero Lint Tolerations**: Code must pass `golangci-lint` with zero errors or warnings (`errcheck`, `goconst`, `lll`, `mnd`, `govet`, etc.).
4. **Security & Vulnerabilities**: Code must pass `govulncheck` and secret scanning via `gitleaks`.

---

## 6. Branch Protection & Pull Request Workflow

> [!IMPORTANT]
> **Direct pushes to the `main` branch are strictly prohibited.** All contributions (including from maintainers) must be submitted via Pull Requests from a feature or fix branch.

### Step-by-Step Contribution Flow:
1. Fork the repository on GitHub (or create a feature branch if you are a collaborator).
2. Create a topic branch from `main`:
   ```bash
   git checkout -b feat/my-awesome-feature
   ```
3. Implement your changes following the Go coding standards and Conventional Commits.
4. Run `make all` locally to ensure all tests, linting, and coverage gates pass:
   ```bash
   make all
   ```
5. Push your branch to GitHub and open a Pull Request against `main`.
6. Complete the PR checklist. Ensure all automated CI status checks pass (`pr-lint`, `lint`, `test`, `coverage >= 95%`, `vulncheck`, `gitleaks`, `build`).
7. Address any code review feedback before merging.
