# groundcover-go developer commands.
#
# `make ci` is the single gate that must pass before any change lands. It runs
# the same steps as the CI workflow: build, vet, lint, and a race-enabled test
# run.

GO ?= go
GOLANGCI_LINT ?= golangci-lint
GOVULNCHECK ?= govulncheck

# Packages of the core module (excludes nested modules under contrib/, etc.).
CORE_PKGS := ./...

.PHONY: ci
ci: build vet lint test-race ## Run the full local CI gate.

.PHONY: build
build: ## Build all core packages.
	$(GO) build $(CORE_PKGS)

.PHONY: vet
vet: ## Run go vet over the core module.
	$(GO) vet $(CORE_PKGS)

.PHONY: fmt
fmt: ## Format all Go sources.
	$(GO) fmt $(CORE_PKGS)

.PHONY: fmt-check
fmt-check: ## Fail if any Go source is not gofmt-clean.
	@unformatted=$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*')); \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi

.PHONY: lint
lint: ## Run golangci-lint over the core module.
	$(GOLANGCI_LINT) run

.PHONY: test
test: ## Run the test suite.
	$(GO) test $(CORE_PKGS)

.PHONY: test-race
test-race: ## Run the test suite with the race detector and coverage.
	$(GO) test -race -covermode=atomic -coverprofile=coverage.txt $(CORE_PKGS)

.PHONY: bench
bench: ## Run benchmarks (overhead gate).
	$(GO) test -run='^$$' -bench=. -benchmem $(CORE_PKGS)

.PHONY: vulncheck
vulncheck: ## Run govulncheck over the core module.
	$(GOVULNCHECK) ./...

.PHONY: tidy
tidy: ## Tidy and vendor the core module dependencies.
	$(GO) mod tidy
	@if [ -d vendor ]; then $(GO) mod vendor; fi

.PHONY: trainer
trainer: ## Run the live round-trip trainer (requires GC_* env vars).
	cd _testdata/trainer && $(GO) run .

.PHONY: help
help: ## Show this help.
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
