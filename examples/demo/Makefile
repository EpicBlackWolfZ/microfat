# ==============================================================================
# Microfat Demo & Benchmark Makefile
# ==============================================================================

GO ?= go
BINARY_NAME ?= demo
BIN_DIR ?= bin
INSTALL_DIR ?= $(HOME)/.local/bin

MICROFAT ?= $(shell command -v microfat 2> /dev/null || echo "$(INSTALL_DIR)/microfat")
MICROFAT_STUB ?= $(shell command -v microfat-stub 2> /dev/null || echo "$(INSTALL_DIR)/microfat-stub")

.PHONY: all build fat trim optimize run bench clean help

all: build fat

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build     Compile standalone native Go binary (default host architecture)"
	@echo "  fat       Compile v1/v3/v4 variants & package universal fat binary into $(BIN_DIR)/demo-fat"
	@echo "  trim      Trim universal fat binary into single-variant $(BIN_DIR)/demo-trimmed (-45% size)"
	@echo "  optimize  Materialize raw uncompressed native v3 ELF into $(BIN_DIR)/demo-optimized"
	@echo "  run       Execute demo application with all benchmark workloads"
	@echo "  bench     Run full 100-iteration statistical benchmark across all 5 binary modes"
	@echo "  clean     Remove compiled binaries and benchmark temporary files"

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
	@echo "==> Running demo workloads..."
	@$(BIN_DIR)/$(BINARY_NAME)-fat all

bench:
	@echo "==> Running full 100-iteration benchmark suite..."
	@cd runner && $(GO) run main.go

clean:
	@echo "==> Cleaning build artifacts..."
	@rm -rf $(BIN_DIR)
