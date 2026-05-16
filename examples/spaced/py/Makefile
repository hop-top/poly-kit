# spaced/py — standalone Python build/test/lint

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@awk 'BEGIN {FS=":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-12s %s\n",$$1,$$2}' $(MAKEFILE_LIST)

.PHONY: start
start: ## Run spaced
	python spaced.py

.PHONY: test
test: ## Run tests
	pytest

.PHONY: lint
lint: ## Ruff check + format
	ruff check . && ruff format --check .

.PHONY: lint-fix
lint-fix: ## Auto-fix
	ruff check --fix . && ruff format .
