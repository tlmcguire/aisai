# AISAI — Go backend + Electron desktop app.
#
# The Go toolchain and Node are not always on PATH on developer machines, so
# the paths below can be overridden:
#
#   make GO=/opt/homebrew/bin/go NPM=/opt/homebrew/bin/npm test
#
# Every target is safe to run repeatedly.

SHELL := /bin/bash

GO      ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)
NPM     ?= $(shell command -v npm 2>/dev/null || echo /usr/local/bin/npm)
NODE    ?= $(shell command -v node 2>/dev/null || echo /usr/local/bin/node)
PYTHON  ?= $(shell command -v python3 2>/dev/null || echo /usr/local/bin/python3)

BIN_DIR := bin
WORKSPACE ?= $(CURDIR)/.aisai

# The package the smoke test audits.
SMOKE_PKG ?= flask
SMOKE_VERSION ?= 2.0.0

# Package identities are resolved by the Go module, not by the network.
GOFLAGS ?=

# The build is stamped with the version, the commit and the build date, so
# that a released binary reports where it came from. When the tree is not a
# git checkout the values fall back to a development marker.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS ?= -X github.com/tlmcguire/aisai/internal/version.Version=$(VERSION) \
           -X github.com/tlmcguire/aisai/internal/version.Commit=$(COMMIT) \
           -X github.com/tlmcguire/aisai/internal/version.Date=$(DATE)

# The linter is pinned so that a local run and a CI run agree.
GOLANGCI_LINT_VERSION ?= v2.15.0
GOLANGCI_LINT ?= $(shell command -v golangci-lint 2>/dev/null || echo $(HOME)/go/bin/golangci-lint)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help.
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- Go backend

.PHONY: build
build: ## Build the server, MCP, benchmark and SBOM binaries into ./bin.
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/aisai-server ./cmd/aisai-server
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/aisai-mcp ./cmd/aisai-mcp
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/aisai-bench ./cmd/aisai-bench
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/aisai-sbom ./cmd/aisai-sbom
	@echo "built: $(BIN_DIR)/aisai-server $(BIN_DIR)/aisai-mcp $(BIN_DIR)/aisai-bench $(BIN_DIR)/aisai-sbom"

.PHONY: build-server
build-server: ## Build only the backend server.
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/aisai-server ./cmd/aisai-server

.PHONY: build-mcp
build-mcp: ## Build only the MCP server.
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/aisai-mcp ./cmd/aisai-mcp

.PHONY: build-bench
build-bench: ## Build only the benchmark harness.
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/aisai-bench ./cmd/aisai-bench

.PHONY: build-sbom
build-sbom: ## Build only the application SBOM generator.
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/aisai-sbom ./cmd/aisai-sbom

.PHONY: test
test: test-go test-electron ## Run every test suite.

.PHONY: test-go
test-go: ## Run the Go unit tests.
	$(GO) test ./...

.PHONY: test-go-race
test-go-race: ## Run the Go unit tests with the race detector.
	$(GO) test -race ./...

.PHONY: cover
cover: ## Run the Go tests and write a coverage profile.
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: vet
vet: ## Run go vet.
	$(GO) vet ./...

.PHONY: fmt
fmt: ## Format the Go sources.
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check: ## Fail if any Go source is not gofmt-clean.
	@unformatted=$$(gofmt -l cmd internal); \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi

.PHONY: tidy
tidy: ## Tidy the Go module.
	$(GO) mod tidy

.PHONY: check
check: fmt-check vet test-go ## Run the full Go quality gate.

.PHONY: lint-go
lint-go: ## Run golangci-lint over the Go tree.
	@if ! command -v $(GOLANGCI_LINT) >/dev/null 2>&1 && [ ! -x "$(GOLANGCI_LINT)" ]; then \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) is not installed."; \
		echo "install it with: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)"; \
		exit 1; \
	fi
	$(GOLANGCI_LINT) run ./...

.PHONY: sbom
sbom: build ## Write the CycloneDX and SPDX SBOMs of the AISAI build itself.
	@mkdir -p $(BIN_DIR)/sbom
	$(BIN_DIR)/aisai-sbom --binary $(BIN_DIR)/aisai-server --both --out-dir $(BIN_DIR)/sbom

.PHONY: version
version: build-server ## Print the stamped version of the server.
	@$(BIN_DIR)/aisai-server --version

.PHONY: test-e2e
test-e2e: ## Run the Playwright end-to-end test of the desktop app.
	cd electron && $(NPM) run test:e2e

.PHONY: smoke
smoke: ## Run the end-to-end smoke test (needs network; no LLM required).
	bash scripts/smoke.sh $(SMOKE_PKG) $(SMOKE_VERSION)

# ---------------------------------------------------------------- run

.PHONY: run
run: ## Run the backend server on a Unix socket under $(WORKSPACE).
	@mkdir -p $(WORKSPACE)
	$(GO) run ./cmd/aisai-server --workspace $(WORKSPACE) --socket $(WORKSPACE)/aisai.sock

.PHONY: run-nollm
run-nollm: ## Run the backend server with LLM features disabled.
	@mkdir -p $(WORKSPACE)
	DISABLE_LLM=1 $(GO) run ./cmd/aisai-server --workspace $(WORKSPACE) --socket $(WORKSPACE)/aisai.sock

# ---------------------------------------------------------------- Electron

.PHONY: install-electron
install-electron: ## Install the Electron dependencies.
	cd electron && $(NPM) install

.PHONY: dev
dev: build ## Run the Electron app in development mode (Vite + Electron).
	cd electron && $(NPM) run dev

.PHONY: dev-backend
dev-backend: build ## Run the backend server separately, for the Electron dev flow.
	@mkdir -p $(WORKSPACE)
	$(GO) run ./cmd/aisai-server --workspace $(WORKSPACE) --socket $(WORKSPACE)/aisai.sock

.PHONY: build-electron
build-electron: build ## Build the production Electron renderer bundle.
	cd electron && $(NPM) run build

.PHONY: typecheck
typecheck: ## Typecheck the Electron renderer.
	cd electron && $(NPM) run typecheck

.PHONY: lint
lint: lint-go lint-electron ## Lint the Go tree and the Electron app.

.PHONY: lint-electron
lint-electron: ## Lint the Electron renderer.
	cd electron && $(NPM) run lint

.PHONY: test-electron
test-electron: ## Run the Electron unit tests.
	cd electron && $(NPM) test

.PHONY: package
package: build build-electron ## Package the desktop app for the current platform.
	cd electron && $(NPM) run package

# ---------------------------------------------------------------- benchmark

.PHONY: bench
bench: build ## Run the benchmark over the bundled corpus.
	$(BIN_DIR)/aisai-bench --input bench/data1551redacted.csv --engine both --out bench/results/bench.csv

.PHONY: bench-quick
bench-quick: build ## Run a small benchmark slice for a fast sanity check.
	$(BIN_DIR)/aisai-bench --input bench/data1551redacted.csv --engine bandit --limit 50 --dry-run --out bench/results/bench-quick.csv

.PHONY: bench-baseline
bench-baseline: build ## Run the trivial baselines for comparison.
	$(BIN_DIR)/aisai-bench --input bench/data1551redacted.csv --baseline always_vulnerable --limit 100 --out bench/results/baseline.csv
	$(BIN_DIR)/aisai-bench --input bench/data1551redacted.csv --baseline always_safe --limit 100 --out bench/results/baseline-safe.csv
	$(BIN_DIR)/aisai-bench --input bench/data1551redacted.csv --baseline keyword --limit 100 --out bench/results/baseline-keyword.csv

.PHONY: bench-tfidf
bench-tfidf: build ## Run the TF-IDF + naive Bayes baseline on a held-out split.
	$(BIN_DIR)/aisai-bench --input bench/data1551redacted.csv --baseline tfidf \
		--tfidf-mode classifier --out bench/results/baseline-tfidf.csv

.PHONY: bench-metrics
bench-metrics: build ## Run the static engine over the whole corpus and write the metrics.
	$(BIN_DIR)/aisai-bench --input bench/data1551redacted.csv --engine bandit \
		--out bench/results/bench-metrics.csv --metrics-out bench/results/metrics.json

.PHONY: bench-nightly
bench-nightly: build ## Run the static engine and compare the metrics with the committed baseline.
	$(BIN_DIR)/aisai-bench --input bench/data1551redacted.csv --engine bandit \
		--out bench/results/bench-nightly.csv --metrics-out bench/results/metrics-nightly.json
	$(PYTHON) .github/workflows/bench-metrics.py \
		bench/results/metrics-nightly.json bench/baseline_metrics.json

.PHONY: bench-external
bench-external: build ## Run the static engine against RealVuln and OWASP BenchmarkPython.
	$(GO) run ./cmd/aisai-bench-external --corpus realvuln --out bench/results/realvuln.json
	$(GO) run ./cmd/aisai-bench-external --corpus benchmarkpython --out bench/results/benchmarkpython.json

.PHONY: bench-baselines
bench-baselines: bench-baseline bench-tfidf ## Run every trivial baseline.

# ---------------------------------------------------------------- MCP

.PHONY: mcp
mcp: build-server ## Print the MCP server configuration for an agent client.
	@echo "Add this to your agent's MCP configuration:"
	@echo ""
	@echo "  {"
	@echo "    \"mcpServers\": {"
	@echo "      \"aisai\": {"
	@echo "        \"command\": \"$(CURDIR)/$(BIN_DIR)/aisai-mcp\","
	@echo "        \"args\": [\"--workspace\", \"$(CURDIR)/$(WORKSPACE)\"]"
	@echo "      }"
	@echo "    }"
	@echo "  }"

.PHONY: mcp-smoke
mcp-smoke: build-server ## Send a single initialize request to the MCP server.
	printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
		| $(BIN_DIR)/aisai-mcp 2>/dev/null

# ---------------------------------------------------------------- housekeeping

.PHONY: clean
clean: ## Remove build output.
	rm -rf $(BIN_DIR) coverage.out
	rm -rf electron/dist

.PHONY: clean-workspace
clean-workspace: ## Remove the local audit workspace.
	rm -rf $(WORKSPACE)

.PHONY: all
all: check lint build test-electron ## Everything CI would run.

# Pause or resume the GitHub Actions workflows (needs an authenticated gh).
.PHONY: ci-pause
ci-pause: ## Disable all CI workflows to stop using runner minutes
	gh workflow disable ci.yml
	gh workflow disable bench-nightly.yml
	gh workflow disable release.yml

.PHONY: ci-resume
ci-resume: ## Re-enable the CI workflows
	gh workflow enable ci.yml
	gh workflow enable bench-nightly.yml
	gh workflow enable release.yml
