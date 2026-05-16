# spaced/ts — standalone TypeScript build/test/lint

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@awk 'BEGIN {FS=":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-12s %s\n",$$1,$$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Type-check
	npx tsc --noEmit --skipLibCheck

.PHONY: start
start: ## Run spaced
	npx tsx spaced.ts

.PHONY: test
test: ## Run tests
	npx vitest run

.PHONY: lint
lint: ## ESLint
	npx eslint .
