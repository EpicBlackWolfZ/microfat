# ==============================================================================
# microfat Makefile
# ==============================================================================

GO ?= go
BIN_DIR ?= bin
COVERAGE_FILE ?= coverage.out

# Version metadata
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS ?= -s -w \
  -X github.com/ghostnetorg/pkg/version.Version=$(VERSION) \
  -X github.com/ghostnetorg/pkg/version.Commit=$(COMMIT) \
  -X github.com/ghostnetorg/pkg/version.Date=$(DATE) \
  -X github.com/ghostnetorg/pkg/version.BuiltBy=makefile \
  -X github.com/ghostnetorg/pkg/version.Vendor=Ghostnet

GOTESTSUM := $(shell command -v gotestsum 2> /dev/null)
GOLANGCI_LINT := $(shell command -v golangci-lint 2> /dev/null)
GOVULNCHECK := $(shell command -v govulncheck 2> /dev/null)
GORELEASER := $(shell command -v goreleaser 2> /dev/null)

.PHONY: all build test coverage lint vuln tidy snapshot demo bench clean help

all: tidy lint vuln test build

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  all        Run tidy, lint, vuln, test, and build"
	@echo "  build      Compile microfat and microfat-stub binaries into $(BIN_DIR)/"
	@echo "  demo       Compile and package the demonstration application in examples/demo"
	@echo "  bench      Run the 100-iteration multi-workload benchmark suite in examples/demo"
	@echo "  test       Run unit tests with race detection"
	@echo "  coverage   Run tests and calculate code coverage"
	@echo "  lint       Run golangci-lint"
	@echo "  vuln       Run govulncheck vulnerability scan"
	@echo "  tidy       Run go mod tidy and go mod verify"
	@echo "  snapshot   Run GoReleaser local snapshot build"
	@echo "  clean      Remove build artifacts and coverage files"

tidy:
	@echo "==> Tidying Go modules..."
	@$(GO) mod tidy
	@$(GO) mod verify

build:
	@echo "==> Building microfat CLI and microfat-stub [$(VERSION)]..."
	@mkdir -p $(BIN_DIR)
	@$(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat ./cmd/microfat
	@GOAMD64=v1 $(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat-stub ./cmd/microfat-stub

demo: build
	@echo "==> Building and packaging examples/demo..."
	@$(MAKE) -C examples/demo fat

bench: build
	@echo "==> Running demo benchmark suite..."
	@$(MAKE) -C examples/demo bench

test:
	@echo "==> Running tests..."
ifdef GOTESTSUM
	@gotestsum -- -race ./...
else
	@$(GO) test -race ./...
endif

coverage:
	@echo "==> Running tests with coverage..."
ifdef GOTESTSUM
	@gotestsum -- -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
else
	@$(GO) test -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
endif
	@echo "==> Total coverage breakdown:"
	@$(GO) tool cover -func=$(COVERAGE_FILE)

lint:
	@echo "==> Running golangci-lint..."
ifdef GOLANGCI_LINT
	@golangci-lint run ./...
else
	@$(GO) vet ./...
endif

vuln:
	@echo "==> Running govulncheck..."
ifdef GOVULNCHECK
	@govulncheck ./...
else
	@$(GO) list -m all > /dev/null
endif

snapshot:
	@echo "==> Testing GoReleaser snapshot build..."
ifdef GORELEASER
	@goreleaser build --snapshot --clean
else
	@echo "--> goreleaser not found in PATH. Please install goreleaser to test snapshot builds."
	@exit 1
endif

clean:
	@echo "==> Cleaning build artifacts..."
	@rm -rf $(BIN_DIR) dist $(COVERAGE_FILE) coverage.html
	@$(MAKE) -C examples/demo clean 2>/dev/null || true
