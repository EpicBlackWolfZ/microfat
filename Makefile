# ==============================================================================
# microfat Makefile
# ==============================================================================

SHELL := /usr/bin/env bash

GO ?= $(shell [ -x "$$(pwd)/.go/bin/go" ] && echo "$$(pwd)/.go/bin/go" || command -v go 2>/dev/null || echo "go")
BIN_DIR ?= bin
COVERAGE_FILE ?= coverage.out
COVERAGE_THRESHOLD ?= 95
HOST_ARCH ?= $(shell $(GO) env GOARCH 2>/dev/null || echo "amd64")

# Version metadata
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS ?= -s -w \
  -X github.com/EpicBlackWolfZ/microfat/internal/version.Version=$(VERSION) \
  -X github.com/EpicBlackWolfZ/microfat/internal/version.Commit=$(COMMIT) \
  -X github.com/EpicBlackWolfZ/microfat/internal/version.Date=$(DATE) \
  -X github.com/EpicBlackWolfZ/microfat/internal/version.BuiltBy=makefile \
  -X github.com/EpicBlackWolfZ/microfat/internal/version.Vendor=EpicBlackWolfZ

GOTESTSUM := $(shell command -v gotestsum 2> /dev/null)
GOLANGCI_LINT := $(shell command -v golangci-lint 2> /dev/null)
GOVULNCHECK := $(shell command -v govulncheck 2> /dev/null)
GORELEASER := $(shell command -v goreleaser 2> /dev/null)

FUZZTIME ?= 5s
PORT ?= 6060
HTTP_PORT ?= 8080
PROFILE ?= heap
DURATION ?= 2s
TRIALS ?= 3

# ==============================================================================
# Color, Formatting & Opt-Out Configuration
# ==============================================================================
# Respect NO_COLOR standard (https://no-color.org) and explicit COLOR=0/false.
# Automatically disables colors and switches symbols to ASCII when stdout is not a TTY.
COLOR ?= 1
ifneq ($(origin NO_COLOR),undefined)
  ifneq ($(NO_COLOR),)
    COLOR := 0
  endif
endif
ifeq ($(COLOR),false)
  COLOR := 0
endif

IS_TTY := $(shell [ -t 2 ] && echo 1 || echo 0)
ifeq ($(IS_TTY),0)
  COLOR := 0
endif

ifeq ($(COLOR),1)
  C_RESET   := \033[0m
  C_BOLD    := \033[1m
  C_CYAN    := \033[36m
  C_GREEN   := \033[32m
  C_YELLOW  := \033[33m
  C_BLUE    := \033[34m
  C_MAGENTA := \033[35m
  C_RED     := \033[31m
  SYM_OK    := ✔
  SYM_FAIL  := ✖
  SYM_WARN  := ⚠
  SYM_ARROW := ==>
else
  C_RESET   :=
  C_BOLD    :=
  C_CYAN    :=
  C_GREEN   :=
  C_YELLOW  :=
  C_BLUE    :=
  C_MAGENTA :=
  C_RED     :=
  SYM_OK    := [OK]
  SYM_FAIL  := [FAIL]
  SYM_WARN  := [WARN]
  SYM_ARROW := ==>
endif

.PHONY: all help build build-amd64 build-arm64 build-all \
        test test-leaks e2e fuzz chaos coverage lint vuln tidy fmt fmt-check fix \
        snapshot demo demo-arm64 demo-check benchmark bench bench-heavy bench-ultra bench-simd bench-startup bench-matrix \
        check-leaks pprof profile-ui test-all clean \
        benchmark-tools benchmark-smoke benchmark-matrix benchmark-integration benchmark-kernel benchmark-kernel-v1

all: tidy fmt-check lint vuln test coverage build ## Run complete verification pipeline (tidy, fmt-check, lint, vuln, test, coverage gate, build)

help: ## Show this help message
	@printf "%b\n" "$(C_BOLD)$(C_CYAN)==============================================================================$(C_RESET)"
	@printf "%b\n" "$(C_BOLD)$(C_CYAN) microfat Development Toolkit$(C_RESET)"
	@printf "%b\n" "$(C_BOLD)$(C_CYAN)==============================================================================$(C_RESET)"
	@printf "%b\n" "$(C_BOLD)Usage:$(C_RESET) make [target] [COLOR=0|1] [NO_COLOR=1]"
	@echo ""
	@printf "%b\n" "$(C_BOLD)Primary Targets:$(C_RESET)"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | grep -v -E '^(bench(-[a-z0-9]+)?|benchmark-[a-z0-9]+):' | awk -v cg="$(C_GREEN)" -v cr="$(C_RESET)" 'BEGIN {FS = ":.*?## "}; {printf "  %s%-20s%s %s\n", cg, $$1, cr, $$2}'
	@echo ""
	@printf "%b\n" "$(C_BOLD)Benchmark Suite Targets:$(C_RESET)"
	@grep -E '^benchmark(-[a-z0-9]+)?:.*?## .*$$' $(MAKEFILE_LIST) | awk -v cy="$(C_CYAN)" -v cr="$(C_RESET)" 'BEGIN {FS = ":.*?## "}; {printf "  %s%-20s%s %s\n", cy, $$1, cr, $$2}'
	@echo ""
	@printf "%b\n" "$(C_BOLD)Legacy / Exploratory Demo Benchmarks (examples/demo):$(C_RESET)"
	@grep -E '^bench(-[a-z0-9]+)?:.*?## .*$$' $(MAKEFILE_LIST) | awk -v cy="$(C_YELLOW)" -v cr="$(C_RESET)" 'BEGIN {FS = ":.*?## "}; {printf "  %s%-20s%s %s\n", cy, $$1, cr, $$2}'
	@echo ""

fmt: ## Format and simplify all Go source files across the codebase
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Formatting Go source files with gofmt -s..."
	@gofmt -s -w .
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) Codebase formatted successfully"

fmt-check: ## Verify all Go source files are formatted with gofmt -s
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Checking Go source code formatting..."
	@UNFORMATTED=$$(gofmt -s -l . 2>/dev/null); \
	if [ -n "$$UNFORMATTED" ]; then \
		printf "%b\n" "$(C_RED)$(SYM_FAIL)$(C_RESET) The following Go files need formatting (run 'make fmt' or 'make fix'):"; \
		printf "%s\n" "$$UNFORMATTED" | sed 's/^/  • /'; \
		exit 1; \
	fi; \
	printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) All Go files formatted cleanly"

fix: ## Run Go modernizer/fixer and golangci-lint automatic fixes, then format
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Applying Go modernizations via 'go fix'..."
	@$(GO) fix ./...
ifdef GOLANGCI_LINT
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Applying automated linter fixes via golangci-lint..."
	@golangci-lint run --fix ./... || true
else
	@printf "%b\n" "$(C_YELLOW)$(SYM_WARN)$(C_RESET) golangci-lint not found in PATH; skipping linter auto-fixes"
endif
	@$(MAKE) fmt
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) Automated fixes and formatting complete"

build: ## Compile microfat CLI and microfat-stub binaries for host architecture into bin/
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Building microfat CLI and microfat-stub [$(VERSION)] for host arch ($(HOST_ARCH))..."
	@mkdir -p $(BIN_DIR)
	@$(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat ./cmd/microfat
ifeq ($(HOST_ARCH),arm64)
	@GOARM64=v8.0 $(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat-stub ./cmd/microfat-stub
	@GOARM64=v8.0 $(GO) build -tags minimal -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat-stub-minimal ./cmd/microfat-stub
else
	@GOAMD64=v1 $(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat-stub ./cmd/microfat-stub
	@GOAMD64=v1 $(GO) build -tags minimal -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat-stub-minimal ./cmd/microfat-stub
endif
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) Binaries built successfully in $(BIN_DIR)/"

build-amd64: ## Cross-compile microfat CLI and stub for Linux AMD64
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Building Linux AMD64 binaries..."
	@mkdir -p $(BIN_DIR)
	@GOOS=linux GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat-amd64 ./cmd/microfat
	@GOOS=linux GOARCH=amd64 GOAMD64=v1 $(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat-stub-amd64 ./cmd/microfat-stub
	@GOOS=linux GOARCH=amd64 GOAMD64=v1 $(GO) build -tags minimal -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat-stub-minimal-amd64 ./cmd/microfat-stub
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) AMD64 binaries built in $(BIN_DIR)/"

build-arm64: ## Cross-compile microfat CLI and stub for Linux ARM64
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Building Linux ARM64 binaries..."
	@mkdir -p $(BIN_DIR)
	@GOOS=linux GOARCH=arm64 $(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat-arm64 ./cmd/microfat
	@GOOS=linux GOARCH=arm64 GOARM64=v8.0 $(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat-stub-arm64 ./cmd/microfat-stub
	@GOOS=linux GOARCH=arm64 GOARM64=v8.0 $(GO) build -tags minimal -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat-stub-minimal-arm64 ./cmd/microfat-stub
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) ARM64 binaries built in $(BIN_DIR)/"

build-all: build build-amd64 build-arm64 ## Build host and all cross-architecture binaries

test: ## Run all default/minimal tests with race detection
	@GO="$(GO)" bash scripts/test-profiles.sh test

e2e: build ## Run end-to-end black-box integration test suite
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Running end-to-end test suite..."
ifdef GOTESTSUM
	@gotestsum -- -race -v ./tests/e2e/...
else
	@$(GO) test -race -v ./tests/e2e/...
endif
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) E2E tests passed successfully"

fuzz: ## Run Go native fuzz testing targets across format, codec, cgroup, and pack
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Running Go native fuzz targets ($(FUZZTIME) per target)..."
	@$(GO) test -fuzz=^FuzzUnmarshalBinaryIndex$$ -fuzztime=$(FUZZTIME) ./internal/format
	@$(GO) test -fuzz=^FuzzUnmarshalJSONIndex$$ -fuzztime=$(FUZZTIME) ./internal/format
	@$(GO) test -fuzz=^FuzzLegacyJSONSyntax$$ -fuzztime=$(FUZZTIME) ./internal/format
	@$(GO) test -fuzz=^FuzzReadTrailerAndIndex$$ -fuzztime=$(FUZZTIME) ./internal/format
	@$(GO) test -fuzz=^FuzzDecompressZstd$$ -fuzztime=$(FUZZTIME) ./internal/codec
	@$(GO) test -fuzz=^FuzzDecompressLZ4$$ -fuzztime=$(FUZZTIME) ./internal/codec
	@$(GO) test -fuzz=^FuzzCalculateGOMEMLIMIT$$ -fuzztime=$(FUZZTIME) ./internal/cgroup
	@$(GO) test -fuzz=^FuzzCalculateGOMAXPROCS$$ -fuzztime=$(FUZZTIME) ./internal/cgroup
	@$(GO) test -fuzz=^FuzzParseGCProfile$$ -fuzztime=$(FUZZTIME) ./internal/cgroup
	@$(GO) test -fuzz=^FuzzResolveTuningPlan$$ -fuzztime=$(FUZZTIME) ./internal/cgroup
	@$(GO) test -fuzz=^FuzzVariantLevelParsing$$ -fuzztime=$(FUZZTIME) ./internal/microarch
	@$(GO) test -fuzz=^FuzzValidateELFBinary$$ -fuzztime=$(FUZZTIME) ./internal/pack
	@$(GO) test -fuzz=^FuzzVerifyBinary$$ -fuzztime=$(FUZZTIME) ./internal/pack
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) Fuzz testing completed cleanly"

chaos: ## Run chaos and fault injection test suite
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Running chaos and fault injection test suite..."
	@$(GO) test -race -run=TestConcurrent ./cmd/microfat-stub/...
	@$(GO) test -race -run=TestCorrupted ./cmd/microfat-stub/...
	@$(GO) test -race -run=TestReadOnly ./cmd/microfat-stub/...
	@$(GO) test -race -run=TestSimulated ./cmd/microfat-stub/...
	@$(GO) test -race -run=TestMalicious ./cmd/microfat-stub/...
	@$(GO) test -race -run=TestTruncated ./internal/pack/...
	@$(GO) test -race -run=TestBitFlipped ./internal/pack/...
	@$(GO) test -race -run=TestDictionaryTampering ./internal/pack/...
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) Chaos tests passed"

test-all: tidy fmt-check lint vuln test e2e chaos coverage build ## Run complete test suite including e2e, chaos, and coverage gate

coverage: ## Union default/minimal atomic coverage and enforce >= 95% overall
	@GO="$(GO)" COVERAGE_FILE="$(COVERAGE_FILE)" COVERAGE_THRESHOLD="$(COVERAGE_THRESHOLD)" bash scripts/test-profiles.sh coverage
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Total coverage breakdown:"
	@$(GO) tool cover -func=$(COVERAGE_FILE)

lint: ## Run golangci-lint across all packages
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Running golangci-lint..."
ifdef GOLANGCI_LINT
	@golangci-lint run ./...
else
	@printf "%b\n" "$(C_YELLOW)$(SYM_WARN)$(C_RESET) golangci-lint not found in PATH, running go vet..."
	@$(GO) vet ./...
endif
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) Linting passed with zero warnings"

vuln: ## Run govulncheck vulnerability scanner
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Running govulncheck..."
ifdef GOVULNCHECK
	@govulncheck ./...
else
	@printf "%b\n" "$(C_YELLOW)$(SYM_WARN)$(C_RESET) govulncheck not found in PATH, verifying modules..."
	@$(GO) list -m all > /dev/null
endif
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) Vulnerability scan clean"

tidy: fmt ## Run go mod tidy, verify module dependencies, and format source code
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Tidying Go modules..."
	@$(GO) mod tidy
	@$(GO) mod verify
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) Modules tidied and verified"

snapshot: ## Test GoReleaser local snapshot build
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Testing GoReleaser snapshot build..."
ifdef GORELEASER
	@goreleaser release --snapshot --clean --skip=publish,sign,announce,validate,sbom
else
	@printf "%b\n" "$(C_RED)$(SYM_FAIL)$(C_RESET) goreleaser not found in PATH. Please install goreleaser to test snapshot builds."
	@exit 1
endif

demo: build ## Build and package the demonstration application in examples/demo
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Building and packaging examples/demo (AMD64)..."
	@$(MAKE) -C examples/demo fat

demo-arm64: build-arm64 ## Build and package the ARM64 demonstration application in examples/demo
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Building and packaging examples/demo (ARM64)..."
	@$(MAKE) -C examples/demo fat-arm64

demo-check: demo ## Verify finished fat binary stub commands (info, optimize, trim, prewarm)
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Verifying finished fat binary stub meta-commands..."
	@$(MAKE) -C examples/demo check
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) All finished binary stub commands verified cleanly"


benchmark: build ## Run canonical microfat benchmark suite and generate reproducible evidence
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Running canonical microfat benchmark suite..."
ifneq ($(BENCHMARK_CONFIG),)
	@BENCHMARK_CONFIG="$(BENCHMARK_CONFIG)" bash scripts/benchmark-ci.sh
else
	@$(BIN_DIR)/microfat benchmark --trials 3 --trial-time 500ms --warmup 200ms
endif

benchmark-tools: ## Explicitly install and verify the pinned external Fortio tool
	@GO="$(GO)" bash scripts/benchmark-tools.sh

benchmark-smoke: build ## Run a paired server benchmark with the installed reference load tool
	@bash scripts/benchmark-ci.sh

benchmark-matrix: build ## Validate all supported format/profile/codec/mode combinations
	@python3 scripts/benchmark-matrix.py compatibility

benchmark-integration: ## Run benchmark integrations with the pinned external tool
	@python3 scripts/benchmark_matrix_test.py
	@python3 scripts/benchmark_evidence_test.py
	@MICROFAT_BENCH_FORTIO="$(CURDIR)/.work/benchmark-tools/fortio" $(GO) test -race ./benchmarks/...

benchmark-kernel: ## Prove native kernel enforcement in an explicitly delegated cgroup
	@MICROFAT_BENCH_REQUIRE_CONTROLS=1 $(GO) test -v ./benchmarks/system -run '^Test(KernelControls|RealCgroupIntegration)$$' -count=1

benchmark-kernel-v1: ## Prove cgroup v1 enforcement in a networkless QEMU TCG guest
	@bash scripts/benchmark-kernel.sh

bench: build ## Run the standard benchmark suite in examples/demo (~110ms)
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Running standard demo benchmark suite..."
	@$(MAKE) -C examples/demo bench

bench-heavy: build ## Run the heavy sustained compute benchmark suite (~500ms) in examples/demo
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Running heavy sustained compute demo benchmark suite..."
	@$(MAKE) -C examples/demo bench-heavy

bench-ultra: build ## Run the ultra heavy sustained compute benchmark suite (5-15s) in examples/demo
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Running ultra heavy sustained compute demo benchmark suite..."
	@$(MAKE) -C examples/demo bench-ultra

bench-simd: build ## Run the SIMD vectorization benchmark suite in examples/demo
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Running SIMD vectorization demo benchmark suite..."
	@$(MAKE) -C examples/demo bench-simd

bench-startup: build ## Run microsecond startup latency and stub telemetry benchmark suite in examples/demo
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Running microsecond startup benchmark suite in examples/demo..."
	@$(MAKE) -C examples/demo bench-startup

bench-matrix: build ## Run comprehensive combinatorial latency matrix benchmark suite in examples/demo
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Running combinatorial startup latency matrix benchmark in examples/demo..."
	@$(MAKE) -C examples/demo bench-startup

check-leaks: build ## Probe Go 1.27 goroutine leak endpoint during benchmark workload
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Launching microfat benchmark with pprof server on port $(PORT)..."
	@bash -c '\
		LOG=$$(mktemp); \
		cleanup() { \
			trap - EXIT INT TERM HUP; \
			rm -f "$$LOG" 2>/dev/null || true; \
			if [ -n "$$PID" ]; then \
				kill "$$PID" 2>/dev/null || true; \
				kill -9 "$$PID" 2>/dev/null || true; \
			fi; \
			if command -v fuser >/dev/null 2>&1; then \
				fuser -k -n tcp $(PORT) 2>/dev/null || true; \
			elif command -v lsof >/dev/null 2>&1; then \
				lsof -ti :$(PORT) | xargs -r kill -9 2>/dev/null || true; \
			fi; \
		}; \
		trap cleanup EXIT INT TERM HUP; \
		if command -v fuser >/dev/null 2>&1; then \
			fuser -k -n tcp $(PORT) 2>/dev/null || true; \
		elif command -v lsof >/dev/null 2>&1; then \
			lsof -ti :$(PORT) | xargs -r kill -9 2>/dev/null || true; \
		fi; \
		MICROFAT_PPROF_PORT=$(PORT) $(BIN_DIR)/microfat benchmark --trials $(TRIALS) --trial-time $(DURATION) --warmup 200ms >"$$LOG" 2>&1 & \
		PID=$$!; \
		READY=0; \
		for i in $$(seq 1 30); do \
			if curl -s "http://localhost:$(PORT)/debug/pprof/" >/dev/null 2>&1; then \
				READY=1; \
				break; \
			fi; \
			if ! kill -0 "$$PID" 2>/dev/null; then \
				break; \
			fi; \
			sleep 0.1; \
		done; \
		if [ $$READY -ne 1 ]; then \
			printf "%b\n" "$(C_RED)$(SYM_FAIL)$(C_RESET) Failed to connect to pprof server on port $(PORT)"; \
			if [ -s "$$LOG" ]; then \
				cat "$$LOG"; \
			fi; \
			cleanup; \
			exit 1; \
		fi; \
		printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Querying Go 1.27 /debug/pprof/goroutineleak endpoint..."; \
		LEAK_REPORT=$$(curl -s "http://localhost:$(PORT)/debug/pprof/goroutineleak?debug=1"); \
		cleanup; \
		printf "%b\n" "$$LEAK_REPORT"; \
		if echo "$$LEAK_REPORT" | grep -q "total 0"; then \
			printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) Goroutine leak check passed: zero leaked goroutines detected"; \
		else \
			printf "%b\n" "$(C_RED)$(SYM_FAIL)$(C_RESET) Goroutine leak check failed: leaked goroutines detected!"; \
			exit 1; \
		fi; \
	'

test-leaks: build ## Run unit tests while probing Go 1.27 /debug/pprof/goroutineleak endpoint
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Running tests with Go 1.27 goroutine leak detection probe..."
	@bash -c '\
		PORT=$(PORT); \
		LOG=$$(mktemp); \
		cleanup() { \
			trap - EXIT INT TERM HUP; \
			rm -f "$$LOG" 2>/dev/null || true; \
			if [ -n "$$TEST_PID" ]; then \
				kill "$$TEST_PID" 2>/dev/null || true; \
				kill -9 "$$TEST_PID" 2>/dev/null || true; \
			fi; \
			if command -v fuser >/dev/null 2>&1; then \
				fuser -k -n tcp $$PORT 2>/dev/null || true; \
			elif command -v lsof >/dev/null 2>&1; then \
				lsof -ti :$$PORT | xargs -r kill -9 2>/dev/null || true; \
			fi; \
		}; \
		trap cleanup EXIT INT TERM HUP; \
		if command -v fuser >/dev/null 2>&1; then \
			fuser -k -n tcp $$PORT 2>/dev/null || true; \
		elif command -v lsof >/dev/null 2>&1; then \
			lsof -ti :$$PORT | xargs -r kill -9 2>/dev/null || true; \
		fi; \
		MICROFAT_PPROF_PORT=$$PORT $(BIN_DIR)/microfat benchmark --trials 20 --trial-time 200ms --warmup 100ms >"$$LOG" 2>&1 & \
		TEST_PID=$$!; \
		READY=0; \
		for i in $$(seq 1 30); do \
			if curl -s "http://localhost:$$PORT/debug/pprof/" >/dev/null 2>&1; then \
				READY=1; \
				break; \
			fi; \
			if ! kill -0 "$$TEST_PID" 2>/dev/null; then \
				break; \
			fi; \
			sleep 0.1; \
		done; \
		if [ $$READY -ne 1 ]; then \
			printf "%b\n" "$(C_RED)$(SYM_FAIL)$(C_RESET) Failed to connect to pprof server on port $$PORT"; \
			if [ -s "$$LOG" ]; then \
				cat "$$LOG"; \
			fi; \
			cleanup; \
			exit 1; \
		fi; \
		printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Querying Go 1.27 /debug/pprof/goroutineleak endpoint..."; \
		LEAK_REPORT=$$(curl -s "http://localhost:$$PORT/debug/pprof/goroutineleak?debug=1"); \
		cleanup; \
		printf "%b\n" "$$LEAK_REPORT"; \
		if ! echo "$$LEAK_REPORT" | grep -q "total 0"; then \
			printf "%b\n" "$(C_RED)$(SYM_FAIL)$(C_RESET) Goroutine leak check failed: leaked goroutines detected!"; \
			exit 1; \
		fi; \
	'
	@GO="$(GO)" bash scripts/test-profiles.sh test
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) Test leak verification completed with zero detected leaks"

pprof: profile-ui ## Alias for profile-ui

profile-ui: build ## Open interactive browser UI for pprof profile (PROFILE=heap|cpu|goroutine|allocs|mutex|block)
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Starting workload with pprof server on port $(PORT) [Profile: $(PROFILE)]..."
	@bash -c '\
		if [ "$(PORT)" = "$(HTTP_PORT)" ]; then \
			printf "%b\n" "$(C_RED)$(SYM_FAIL)$(C_RESET) PORT ($(PORT)) and HTTP_PORT ($(HTTP_PORT)) cannot be the same"; \
			exit 1; \
		fi; \
		LOG=$$(mktemp); \
		cleanup() { \
			trap - EXIT INT TERM HUP; \
			rm -f "$$LOG" 2>/dev/null || true; \
			if [ -n "$$PID" ]; then \
				kill "$$PID" 2>/dev/null || true; \
				kill -9 "$$PID" 2>/dev/null || true; \
			fi; \
			if command -v fuser >/dev/null 2>&1; then \
				fuser -k -n tcp $(PORT) 2>/dev/null || true; \
				fuser -k -n tcp $(HTTP_PORT) 2>/dev/null || true; \
			elif command -v lsof >/dev/null 2>&1; then \
				lsof -ti :$(PORT) | xargs -r kill -9 2>/dev/null || true; \
				lsof -ti :$(HTTP_PORT) | xargs -r kill -9 2>/dev/null || true; \
			fi; \
		}; \
		trap cleanup EXIT INT TERM HUP; \
		if command -v fuser >/dev/null 2>&1; then \
			fuser -k -n tcp $(PORT) 2>/dev/null || true; \
			fuser -k -n tcp $(HTTP_PORT) 2>/dev/null || true; \
		elif command -v lsof >/dev/null 2>&1; then \
			lsof -ti :$(PORT) | xargs -r kill -9 2>/dev/null || true; \
			lsof -ti :$(HTTP_PORT) | xargs -r kill -9 2>/dev/null || true; \
		fi; \
		PNAME="$(PROFILE)"; \
		PNAME="$$(echo "$$PNAME" | tr "[:upper:]" "[:lower:]")"; \
		if [ "$$PNAME" = "cpu" ]; then PNAME="profile"; fi; \
		SEC="$(DURATION)"; \
		SEC="$${SEC%s}"; \
		case "$$SEC" in \
			""|*[!0-9]*) SEC=2 ;; \
		esac; \
		EXTRA_PARAM=""; \
		if [ "$$PNAME" = "profile" ]; then EXTRA_PARAM="?seconds=$$SEC"; fi; \
		MICROFAT_PPROF_PORT=$(PORT) $(BIN_DIR)/microfat benchmark --trials 50 --trial-time 1s --warmup 200ms >"$$LOG" 2>&1 & \
		PID=$$!; \
		READY=0; \
		for i in $$(seq 1 30); do \
			if curl -s "http://localhost:$(PORT)/debug/pprof/" >/dev/null 2>&1; then \
				READY=1; \
				break; \
			fi; \
			if ! kill -0 "$$PID" 2>/dev/null; then \
				break; \
			fi; \
			sleep 0.1; \
		done; \
		if [ $$READY -ne 1 ]; then \
			printf "%b\n" "$(C_RED)$(SYM_FAIL)$(C_RESET) Failed to connect to pprof server on port $(PORT)"; \
			if [ -s "$$LOG" ]; then \
				cat "$$LOG"; \
			fi; \
			cleanup; \
			exit 1; \
		fi; \
		printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) Pprof server listening at http://localhost:$(PORT)/debug/pprof/"; \
		printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Launching interactive pprof web UI at http://localhost:$(HTTP_PORT)..."; \
		$(GO) tool pprof -http=:$(HTTP_PORT) "http://localhost:$(PORT)/debug/pprof/$$PNAME$$EXTRA_PARAM" || true; \
		cleanup; \
	'

clean: ## Remove build artifacts and coverage files
	@printf "%b\n" "$(C_BLUE)$(SYM_ARROW)$(C_RESET) Cleaning build artifacts..."
	@rm -rf $(BIN_DIR) dist $(COVERAGE_FILE) coverage.html unit-tests.xml
	@$(MAKE) -C examples/demo clean 2>/dev/null || true
	@printf "%b\n" "$(C_GREEN)$(SYM_OK)$(C_RESET) Clean complete"
