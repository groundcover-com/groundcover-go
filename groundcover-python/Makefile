# Development entry points. `make ci` is the gate for every change.

UV ?= uv

.PHONY: ci sync lint fmt test build clean

ci: sync lint test ## Everything CI runs

sync: ## Install/refresh the dev environment
	$(UV) sync --all-extras

lint: ## Static checks
	$(UV) run ruff check src tests examples
	$(UV) run ruff format --check src tests examples

fmt: ## Auto-format
	$(UV) run ruff format src tests examples
	$(UV) run ruff check --fix src tests examples

test: ## Run the test suite
	$(UV) run pytest

build: ## Build sdist + wheel
	$(UV) build

clean:
	rm -rf dist build .pytest_cache .ruff_cache
