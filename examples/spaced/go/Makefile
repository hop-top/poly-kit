# spaced/go — standalone Go build/test/lint
# Go build runs from repo root (go.mod lives there).

REPO_ROOT := $(abspath ../../..)

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@awk 'BEGIN {FS=":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-12s %s\n",$$1,$$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build binary
	cd $(REPO_ROOT) && go build -buildvcs=false -o examples/spaced/go/spaced ./examples/spaced/go/

.PHONY: start
start: build ## Run spaced
	./spaced $(ARGS)

.PHONY: test
test: ## Run tests
	cd $(REPO_ROOT) && go test ./examples/spaced/go/...

.PHONY: lint
lint: ## Run golangci-lint
	cd $(REPO_ROOT) && golangci-lint run ./examples/spaced/go/...

.PHONY: clean
clean: ## Remove binary
	rm -f spaced
