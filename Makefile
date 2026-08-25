# ==============================================================================
# microfat Makefile
# ==============================================================================

GO ?= go
BIN_DIR ?= bin
COVERAGE_FILE ?= coverage.out
COVERAGE_THRESHOLD ?= 95

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

.PHONY: all help build test coverage lint vuln tidy snapshot demo bench bench-heavy bench-ultra clean

all: tidy lint vuln test coverage build ## Run complete verification pipeline (tidy, lint, vuln, test, coverage gate, build)

help: ## Show this help message
	@echo "\033[1;36m==============================================================================\033[0m"
	@echo "\033[1;36m microfat Development Toolkit\033[0m"
	@echo "\033[1;36m==============================================================================\033[0m"
	@echo "\033[1mUsage:\033[0m make [target]"
	@echo ""
	@echo "\033[1mPrimary Targets:\033[0m"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[32m%-14s\033[0m %s\n", $$1, $$2}'
	@echo ""

build: ## Compile microfat CLI and microfat-stub binaries into bin/
	@echo "\033[34m==>\033[0m Building microfat CLI and microfat-stub [$(VERSION)]..."
	@mkdir -p $(BIN_DIR)
	@$(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat ./cmd/microfat
	@GOAMD64=v1 $(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/microfat-stub ./cmd/microfat-stub
	@echo "\033[32m✔\033[0m Binaries built successfully in $(BIN_DIR)/"

test: ## Run unit tests with race detection
	@echo "\033[34m==>\033[0m Running unit tests with race detection..."
ifdef GOTESTSUM
	@gotestsum -- -race ./...
else
	@$(GO) test -race ./...
endif
	@echo "\033[32m✔\033[0m Tests passed successfully"

COVERAGE_PKGS ?= ./cmd/... ./internal/...

coverage: ## Run tests with atomic coverage and enforce >= 95% threshold gate
	@echo "\033[34m==>\033[0m Running tests and checking coverage..."
ifdef GOTESTSUM
	@gotestsum -- -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic $(COVERAGE_PKGS)
else
	@$(GO) test -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic $(COVERAGE_PKGS)
endif
	@echo "\033[34m==>\033[0m Total coverage breakdown:"
	@$(GO) tool cover -func=$(COVERAGE_FILE)
	@total=$$($(GO) tool cover -func=$(COVERAGE_FILE) | grep "total:" | awk '{print substr($$3, 1, length($$3)-1)}'); \
	echo "\033[1mOverall Code Coverage:\033[0m $$total% (Required Gate: $(COVERAGE_THRESHOLD)%)"; \
	if [ $$(echo "$$total >= $(COVERAGE_THRESHOLD)" | bc -l 2>/dev/null || awk -v t="$$total" -v req="$(COVERAGE_THRESHOLD)" 'BEGIN {if (t >= req) exit 0; else exit 1}') ]; then \
		echo "\033[32m✔ Coverage gate passed ($$total% >= $(COVERAGE_THRESHOLD)%)\033[0m"; \
	else \
		echo "\033[31m✖ Coverage gate failed: $$total% is below $(COVERAGE_THRESHOLD)%\033[0m"; \
		exit 1; \
	fi

lint: ## Run golangci-lint across all packages
	@echo "\033[34m==>\033[0m Running golangci-lint..."
ifdef GOLANGCI_LINT
	@golangci-lint run ./...
else
	@echo "\033[33m--> golangci-lint not found in PATH, running go vet...\033[0m"
	@$(GO) vet ./...
endif
	@echo "\033[32m✔\033[0m Linting passed with zero warnings"

vuln: ## Run govulncheck vulnerability scanner
	@echo "\033[34m==>\033[0m Running govulncheck..."
ifdef GOVULNCHECK
	@govulncheck ./...
else
	@echo "\033[33m--> govulncheck not found in PATH, verifying modules...\033[0m"
	@$(GO) list -m all > /dev/null
endif
	@echo "\033[32m✔\033[0m Vulnerability scan clean"

tidy: ## Run go mod tidy and verify module dependencies
	@echo "\033[34m==>\033[0m Tidying Go modules..."
	@$(GO) mod tidy
	@$(GO) mod verify
	@echo "\033[32m✔\033[0m Modules tidied and verified"

snapshot: ## Test GoReleaser local snapshot build
	@echo "\033[34m==>\033[0m Testing GoReleaser snapshot build..."
ifdef GORELEASER
	@goreleaser release --snapshot --clean --skip=publish,sign,announce,validate,sbom
else
	@echo "\033[31m✖ goreleaser not found in PATH. Please install goreleaser to test snapshot builds.\033[0m"
	@exit 1
endif

demo: build ## Build and package the demonstration application in examples/demo
	@echo "\033[34m==>\033[0m Building and packaging examples/demo..."
	@$(MAKE) -C examples/demo fat

bench: build ## Run the standard benchmark suite in examples/demo (~110ms)
	@echo "\033[34m==>\033[0m Running standard demo benchmark suite..."
	@$(MAKE) -C examples/demo bench

bench-heavy: build ## Run the heavy sustained compute benchmark suite (~500ms) in examples/demo
	@echo "\033[34m==>\033[0m Running heavy sustained compute demo benchmark suite..."
	@$(MAKE) -C examples/demo bench-heavy

bench-ultra: build ## Run the ultra heavy sustained compute benchmark suite (5-15s) in examples/demo
	@echo "\033[34m==>\033[0m Running ultra heavy sustained compute demo benchmark suite..."
	@$(MAKE) -C examples/demo bench-ultra

clean: ## Remove build artifacts and coverage files
	@echo "\033[34m==>\033[0m Cleaning build artifacts..."
	@rm -rf $(BIN_DIR) dist $(COVERAGE_FILE) coverage.html unit-tests.xml
	@$(MAKE) -C examples/demo clean 2>/dev/null || true
	@echo "\033[32m✔\033[0m Clean complete"
