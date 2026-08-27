# ==============================================================================
# Microfat Demo & Benchmark Makefile
# ==============================================================================

GO ?= $(shell [ -x "$$(cd ../.. 2>/dev/null && pwd)/.go/bin/go" ] && echo "$$(cd ../.. 2>/dev/null && pwd)/.go/bin/go" || command -v go 2>/dev/null || echo "go")
BINARY_NAME ?= demo
BIN_DIR ?= bin
INSTALL_DIR ?= $(HOME)/.local/bin

REPO_ROOT_BIN ?= $(shell cd ../.. 2>/dev/null && pwd)/bin
MICROFAT ?= $(shell [ -x "$$(cd ../.. 2>/dev/null && pwd)/bin/microfat" ] && echo "$$(cd ../.. 2>/dev/null && pwd)/bin/microfat" || command -v microfat 2> /dev/null || echo "$(INSTALL_DIR)/microfat")
MICROFAT_STUB ?= $(shell [ -x "$$(cd ../.. 2>/dev/null && pwd)/bin/microfat-stub" ] && echo "$$(cd ../.. 2>/dev/null && pwd)/bin/microfat-stub" || command -v microfat-stub 2> /dev/null || echo "$(INSTALL_DIR)/microfat-stub")
MICROFAT_STUB_ARM64 ?= $(shell [ -x "$$(cd ../.. 2>/dev/null && pwd)/bin/microfat-stub-arm64" ] && echo "$$(cd ../.. 2>/dev/null && pwd)/bin/microfat-stub-arm64" || echo "$(BIN_DIR)/microfat-stub-arm64")

.PHONY: all build fat fat-arm64 trim optimize run run-heavy run-ultra bench bench-heavy bench-ultra bench-startup clean help

all: build fat

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build        Compile standalone native Go binary (default host architecture)"
	@echo "  fat          Compile AMD64 v1/v2/v3/v4 variants & package universal fat binary into $(BIN_DIR)/demo-fat"
	@echo "  fat-arm64    Compile ARM64 v8.0/v8.2/v9.0 variants & package universal fat binary into $(BIN_DIR)/demo-fat-arm64"
	@echo "  trim         Trim universal fat binary into single-variant $(BIN_DIR)/demo-trimmed (-45% size)"
	@echo "  optimize     Materialize raw uncompressed native v3 ELF into $(BIN_DIR)/demo-optimized"
	@echo "  run          Execute demo application with standard benchmark workloads (~110ms)"
	@echo "  run-heavy    Execute demo application with heavy sustained compute workloads (~500ms)"
	@echo "  run-ultra    Execute demo application with ultra heavy sustained compute workloads (5-15s)"
	@echo "  bench        Run standard 50-iteration benchmark suite across all 5 binary modes"
	@echo "  bench-heavy  Run heavy sustained compute benchmark suite (20 iterations) across all 5 modes"
	@echo "  bench-ultra  Run ultra heavy sustained compute benchmark suite (5-15s per run) across all 5 modes"
	@echo "  bench-startup Run microsecond startup overhead benchmark suite across execution modes & codecs"
	@echo "  clean        Remove compiled binaries and benchmark temporary files"

build:
	@echo "==> Building native binary..."
	@mkdir -p $(BIN_DIR)
	@$(GO) build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME) main.go

fat:
	@echo "==> Compiling AMD64 microarchitecture variants (v1, v2, v3, v4)..."
	@mkdir -p $(BIN_DIR)
	@GOAMD64=v1 $(GO) build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME)_v1 main.go
	@GOAMD64=v2 $(GO) build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME)_v2 main.go
	@GOAMD64=v3 $(GO) build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME)_v3 main.go
	@GOAMD64=v4 $(GO) build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME)_v4 main.go
	@echo "==> Packaging self-dispatching microfat binary..."
	@$(MICROFAT) pack \
		--stub $(MICROFAT_STUB) \
		--name $(BINARY_NAME) \
		-v v1=$(BIN_DIR)/$(BINARY_NAME)_v1 \
		-v v2=$(BIN_DIR)/$(BINARY_NAME)_v2 \
		-v v3=$(BIN_DIR)/$(BINARY_NAME)_v3 \
		-v v4=$(BIN_DIR)/$(BINARY_NAME)_v4 \
		-o $(BIN_DIR)/$(BINARY_NAME)-fat
	@echo "==> Fat binary created at $(BIN_DIR)/$(BINARY_NAME)-fat!"

fat-arm64:
	@echo "==> Compiling ARM64 microarchitecture variants (v8.0, v8.2, v9.0)..."
	@mkdir -p $(BIN_DIR)
	@GOOS=linux GOARCH=arm64 GOARM64=v8.0 $(GO) build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME)_arm64_v8.0 main.go
	@GOOS=linux GOARCH=arm64 GOARM64=v8.2 $(GO) build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME)_arm64_v8.2 main.go
	@GOOS=linux GOARCH=arm64 GOARM64=v9.0 $(GO) build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME)_arm64_v9.0 main.go
	@GOOS=linux GOARCH=arm64 GOARM64=v8.0 $(GO) build -ldflags="-s -w" -o $(BIN_DIR)/microfat-stub-arm64 ../../cmd/microfat-stub
	@echo "==> Packaging self-dispatching ARM64 microfat binary..."
	@$(MICROFAT) pack \
		--stub $(BIN_DIR)/microfat-stub-arm64 \
		--name $(BINARY_NAME) \
		--arch arm64 \
		-v v8.0=$(BIN_DIR)/$(BINARY_NAME)_arm64_v8.0 \
		-v v8.2=$(BIN_DIR)/$(BINARY_NAME)_arm64_v8.2 \
		-v v9.0=$(BIN_DIR)/$(BINARY_NAME)_arm64_v9.0 \
		-o $(BIN_DIR)/$(BINARY_NAME)-fat-arm64
	@echo "==> ARM64 Fat binary created at $(BIN_DIR)/$(BINARY_NAME)-fat-arm64!"

trim: fat
	@echo "==> Creating trimmed fat binary..."
	@$(MICROFAT) trim $(BIN_DIR)/$(BINARY_NAME)-fat -o $(BIN_DIR)/$(BINARY_NAME)-trimmed
	@echo "==> Trimmed fat binary created at $(BIN_DIR)/$(BINARY_NAME)-trimmed!"

optimize: fat
	@echo "==> Materializing raw native ELF..."
	@$(BIN_DIR)/$(BINARY_NAME)-fat --microfat:optimize-to=$(BIN_DIR)/$(BINARY_NAME)-optimized
	@echo "==> Raw native ELF materialized at $(BIN_DIR)/$(BINARY_NAME)-optimized!"

run: fat
	@echo "==> Running standard demo workloads..."
	@$(BIN_DIR)/$(BINARY_NAME)-fat all

run-heavy: fat
	@echo "==> Running heavy sustained compute workloads (~500ms)..."
	@$(BIN_DIR)/$(BINARY_NAME)-fat all --heavy

run-ultra: fat
	@echo "==> Running ultra heavy sustained compute workloads (5-15s)..."
	@$(BIN_DIR)/$(BINARY_NAME)-fat all --ultra

bench:
	@echo "==> Running standard benchmark suite..."
	@cd runner && $(GO) run main.go

bench-heavy:
	@echo "==> Running heavy sustained compute benchmark suite..."
	@cd runner && $(GO) run main.go -heavy

bench-ultra:
	@echo "==> Running ultra heavy sustained compute benchmark suite..."
	@cd runner && $(GO) run main.go -ultra

bench-startup:
	@echo "==> Running microsecond startup overhead & telemetry benchmark suite..."
	@cd runner && $(GO) run main.go -startup

clean:
	@echo "==> Cleaning build artifacts..."
	@rm -rf $(BIN_DIR)
