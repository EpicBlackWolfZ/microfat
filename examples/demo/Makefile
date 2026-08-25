# ==============================================================================
# Microfat Demo & Benchmark Makefile
# ==============================================================================

GO ?= go
BINARY_NAME ?= demo
BIN_DIR ?= bin
INSTALL_DIR ?= $(HOME)/.local/bin

MICROFAT ?= $(shell command -v microfat 2> /dev/null || echo "$(INSTALL_DIR)/microfat")
MICROFAT_STUB ?= $(shell command -v microfat-stub 2> /dev/null || echo "$(INSTALL_DIR)/microfat-stub")

.PHONY: all build fat trim optimize run run-heavy run-ultra bench bench-heavy bench-ultra clean help

all: build fat

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build        Compile standalone native Go binary (default host architecture)"
	@echo "  fat          Compile v1/v3/v4 variants & package universal fat binary into $(BIN_DIR)/demo-fat"
	@echo "  trim         Trim universal fat binary into single-variant $(BIN_DIR)/demo-trimmed (-45% size)"
	@echo "  optimize     Materialize raw uncompressed native v3 ELF into $(BIN_DIR)/demo-optimized"
	@echo "  run          Execute demo application with standard benchmark workloads (~110ms)"
	@echo "  run-heavy    Execute demo application with heavy sustained compute workloads (~500ms)"
	@echo "  run-ultra    Execute demo application with ultra heavy sustained compute workloads (5-15s)"
	@echo "  bench        Run standard 50-iteration benchmark suite across all 5 binary modes"
	@echo "  bench-heavy  Run heavy sustained compute benchmark suite (20 iterations) across all 5 modes"
	@echo "  bench-ultra  Run ultra heavy sustained compute benchmark suite (5-15s per run) across all 5 modes"
	@echo "  clean        Remove compiled binaries and benchmark temporary files"

build:
	@echo "==> Building native binary..."
	@mkdir -p $(BIN_DIR)
	@$(GO) build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME) main.go

fat:
	@echo "==> Compiling microarchitecture variants (v1, v3, v4)..."
	@mkdir -p $(BIN_DIR)
	@GOAMD64=v1 $(GO) build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME)_v1 main.go
	@GOAMD64=v3 $(GO) build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME)_v3 main.go
	@GOAMD64=v4 $(GO) build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME)_v4 main.go
	@echo "==> Packaging self-dispatching microfat binary..."
	@$(MICROFAT) pack \
		--stub $(MICROFAT_STUB) \
		--name $(BINARY_NAME) \
		-v v1=$(BIN_DIR)/$(BINARY_NAME)_v1 \
		-v v3=$(BIN_DIR)/$(BINARY_NAME)_v3 \
		-v v4=$(BIN_DIR)/$(BINARY_NAME)_v4 \
		-o $(BIN_DIR)/$(BINARY_NAME)-fat
	@echo "==> Fat binary created at $(BIN_DIR)/$(BINARY_NAME)-fat!"

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

clean:
	@echo "==> Cleaning build artifacts..."
	@rm -rf $(BIN_DIR)
