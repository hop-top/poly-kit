# spaced — satirical SpaceX CLI historian
# Parity test vehicle for hop.top/kit/cli (Go · TypeScript · Python · Web)
#
# All paths relative to this Makefile (examples/spaced/).
# Go build MUST run from repo root (go.mod lives there).

REPO_ROOT := $(abspath ../..)
SPACED_DIR := $(abspath .)

GO_ENTRY    := ./examples/spaced/go/
TS_ENTRY    := $(SPACED_DIR)/ts/spaced.ts
PY_VENV     := $(REPO_ROOT)/sdk/py/.venv
PY_ENTRY    := $(SPACED_DIR)/py/spaced.py
WEB_DIR     := $(SPACED_DIR)/web
WEB_PORT    := 3131

# Node: shared node_modules live two levels up in sdk/ts/
NODE_PATH   := $(REPO_ROOT)/sdk/ts/node_modules/.bin

.DEFAULT_GOAL := help

# ── Help ──────────────────────────────────────────────────────────────────────

.PHONY: help
help: ## List all targets with descriptions
	@echo "spaced — satirical SpaceX CLI historian"
	@echo ""
	@echo "Usage: make <target>"
	@echo ""
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ { printf "  %-15s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ── Build ─────────────────────────────────────────────────────────────────────

.PHONY: build
build: build-go build-ts build-py build-web ## Build all 4 implementations

.PHONY: build-go
build-go: ## Build Go binary (runs from repo root where go.mod lives; output: bin/spaced)
	@mkdir -p $(SPACED_DIR)/bin
	cd $(REPO_ROOT) && go build -buildvcs=false -o $(SPACED_DIR)/bin/spaced $(GO_ENTRY)

.PHONY: build-ts
build-ts: ## Type-check TypeScript entry (tsx runs JIT; no compile artifact)
	cd $(REPO_ROOT)/sdk/ts && npx tsc --noEmit --skipLibCheck \
		--moduleResolution bundler --target esnext --module esnext \
		$(SPACED_DIR)/ts/spaced.ts

.PHONY: build-py
build-py: ## Validate Python entry (syntax check via py_compile)
	$(PY_VENV)/bin/python -m py_compile $(PY_ENTRY)

.PHONY: build-web
build-web: ## Bundle web demo via esbuild (output: web/web.js)
	cd $(WEB_DIR) && npm run build

# ── Run ───────────────────────────────────────────────────────────────────────

.PHONY: run-go
run-go: build-go ## Run Go binary (pass ARGS="..." for subcommands)
	$(SPACED_DIR)/bin/spaced $(ARGS)

.PHONY: run-ts
run-ts: ## Run TypeScript CLI via tsx (pass ARGS="..." for subcommands)
	cd $(SPACED_DIR) && npx tsx --tsconfig /dev/null \
		--experimental-specifier-resolution=node \
		$(TS_ENTRY) $(ARGS)

.PHONY: run-py
run-py: ## Run Python CLI via venv (pass ARGS="..." for subcommands)
	$(PY_VENV)/bin/python $(PY_ENTRY) $(ARGS)

# ── Web ───────────────────────────────────────────────────────────────────────

.PHONY: serve-web
serve-web: build-web ## Serve browser demo on port 3131
	cd $(WEB_DIR) && npx serve . --listen $(WEB_PORT)

# ── Test ──────────────────────────────────────────────────────────────────────

.PHONY: test
test: ## Run cross-language parity tests (from repo root)
	cd $(REPO_ROOT) && go test -tags parity ./cli/... -v -run TestParity

.PHONY: test-web
test-web: build-web ## Run Playwright tests for web demo
	cd $(WEB_DIR) && npx playwright test

# ── Media ─────────────────────────────────────────────────────────────────────

TAPES_DIR   := $(SPACED_DIR)/tapes
MEDIA_DIR   := $(SPACED_DIR)/media
SCREENSHOT  := $(WEB_DIR)/scripts/screenshot.mjs

.PHONY: screenshots
screenshots: build-web ## Take web screenshots (saved to media/)
	node $(SCREENSHOT)

.PHONY: record-go
record-go: build-go ## Record Go CLI demo (vhs → media/go.gif)
	cd $(TAPES_DIR) && vhs go.tape

.PHONY: record-ts
record-ts: ## Record TypeScript CLI demo (vhs → media/ts.gif)
	cd $(TAPES_DIR) && vhs ts.tape

.PHONY: record-py
record-py: ## Record Python CLI demo (vhs → media/py.gif)
	cd $(TAPES_DIR) && vhs py.tape

.PHONY: record
record: record-go record-ts record-py ## Record all CLI demos

.PHONY: media
media: screenshots record ## Generate all media (screenshots + recordings)

# ── Lint ──────────────────────────────────────────────────────────────────────

.PHONY: lint
lint: lint-md lint-links lint-shell ## Run all linters

.PHONY: lint-md
lint-md: ## Lint markdown files
	npx markdownlint-cli2 "**/*.md" "#node_modules"

.PHONY: lint-links
lint-links: ## Check for broken links in docs
	lychee --config lychee.toml docs/ README.md

.PHONY: lint-shell
lint-shell: ## ShellCheck tape scripts
	shellcheck tapes/*.tape 2>/dev/null || true

# ── Clean ─────────────────────────────────────────────────────────────────────

.PHONY: clean
clean: ## Remove build artifacts (Go binary, web bundle)
	-rm -rf $(SPACED_DIR)/bin
	-rm -f $(REPO_ROOT)/spaced
	-rm -f $(WEB_DIR)/web.js $(WEB_DIR)/web.js.map
