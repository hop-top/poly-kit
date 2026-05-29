.PHONY: setup lint lint-go lint-ts lint-py lint-rs lint-docs lint-config lint-links lint-sdk-paths \
	preflight \
	tools tools-golangci-lint \
	test test-go test-go-integration test-ts test-py test-rs test-parity test-parity-typeid \
	proto openapi clients clients-ts clients-php clients-rs clients-test api \
	job-test job-integration-hatchet job-integration-restate job-integration-temporal \
	test-workflow test-hook test-release promote promote-alpha promote-beta promote-rc promote-release check \
	test-templates lint-templates build builtins-sync check-mirror-sync refresh-secret-rules \
	refresh-pii-rules refresh-rules

# Tool versions — single source of truth for local + the kit repo's
# own CI. `.github/workflows/ci.yml` consumes the pin by calling
# `make lint-go`, which in turn depends on `tools-golangci-lint`.
#
# Note: `.github/workflows/lint.yml` is a reusable workflow exposed
# to OTHER hop-top repos via `workflow_call` and takes its own
# `inputs.version` independently — it is NOT covered by this pin.
GOLANGCI_LINT_VERSION ?= v2.11.4

# lint-go invokes the binary from $(LOCAL_BIN) directly. The
# `tools-golangci-lint` target is a hard dep, so the binary is
# guaranteed present by the time the recipe runs. Using an
# absolute path avoids PATH manipulation in `find -execdir`
# subshells (which would otherwise fall back to the PATH-installed
# version, defeating the version pin).
LOCAL_BIN := $(CURDIR)/bin
GOLANGCI_LINT := $(LOCAL_BIN)/golangci-lint

preflight: ## Verify host toolchain matches the repo's declared minimum reqs
	@scripts/preflight.sh

tools: tools-golangci-lint ## Install pinned dev tools into bin/

tools-golangci-lint: ## Install the pinned golangci-lint version into bin/
	@mkdir -p $(LOCAL_BIN)
	@if [ ! -x $(LOCAL_BIN)/golangci-lint ] || ! $(LOCAL_BIN)/golangci-lint version 2>/dev/null | grep -q "$(GOLANGCI_LINT_VERSION:v%=%)"; then \
		echo "==> Installing golangci-lint $(GOLANGCI_LINT_VERSION) into $(LOCAL_BIN)"; \
		GOBIN=$(LOCAL_BIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	else \
		echo "==> golangci-lint $(GOLANGCI_LINT_VERSION) already installed"; \
	fi

setup: preflight ## Initialize all sub-projects and dependencies
	@echo "==> Initializing Go modules"
	go mod download
	@echo "==> Initializing TypeScript SDK"
	cd sdk/ts && pnpm install
	@echo "==> Initializing Python SDK"
	cd sdk/py && uv sync --all-extras
	@echo "==> Initializing Python Engine SDK"
	cd engine/sdk/py-kit-engine && uv sync --all-extras
	@echo "==> Setup complete."

check: preflight lint test ## Run all linters and tests (full gate)

test: preflight test-go test-ts test-py test-workflow test-hook ## Run all tests

test-go: ## Go tests (skips long-running container tests)
	@go test -short ./... -count=1 -timeout 1200s
	@find go cmd contracts engine examples incubator -name "go.mod" -execdir go test -short ./... -count=1 -timeout 1200s \;

test-go-integration: ## Go tests including testcontainer integration
	@go test ./... -count=1 -timeout 1200s
	@find go cmd contracts engine examples incubator -name "go.mod" -execdir go test ./... -count=1 -timeout 1200s \;

test-ts: ## TypeScript tests
	cd sdk/ts && pnpm vitest run --exclude src/sqlstore.test.ts
	cd engine/sdk/ts-kit-engine && ./node_modules/.bin/vitest run

test-py: ## Python tests
	cd sdk/py && uv sync --all-extras -q && uv run pytest
	cd engine/sdk/py-kit-engine && uv sync --all-extras -q && uv run pytest

test-rs: ## Rust tests (default + all features, matches publish-rs.yml + manual --features api)
	# Default features must compile and pass — that's what publish-rs.yml
	# runs by default ('cargo test'). Without this, a source file gated on
	# the api feature could break default build and only fail at publish.
	cd sdk/experimental/rs && cargo test --locked
	# All features ensures the api integration test (gated on feature=api)
	# is exercised at PR time, not deferred to manual runs.
	cd sdk/experimental/rs && cargo test --all-features --locked

test-parity: test-parity-typeid ## Cross-language parity tests
	go test -tags parity ./go/console/cli/... -timeout 300s -count=1
	cd engine/sdk/py-kit-engine && uv sync --all-extras -q
	go test -tags parity ./engine/sdk/parity/... -timeout 300s -count=1

# Per-SDK loaders for contracts/typeid-v1/fixtures.json. Each SDK runs
# its own contract test; a wire-incompatible change in any SDK (or in
# the shared fixture file) fails this target. PHP is treated as
# optional because the kit's PHP toolchain is experimental and not
# every CI runner ships it — when `php` and `composer` are present we
# run the PHP contract test too. tlc T-0753.
test-parity-typeid: ## TypeID v1 contract loaders across all 5 SDKs
	@echo "==> typeid-v1 parity: Go"
	go test ./go/core/id/... -run '^TestContract' -count=1 -timeout 60s
	@echo "==> typeid-v1 parity: Rust"
	cd sdk/experimental/rs && cargo test --features id --test contract --locked
	@echo "==> typeid-v1 parity: TypeScript"
	cd sdk/ts && pnpm vitest run src/id/contract.test.ts
	@echo "==> typeid-v1 parity: Python"
	cd sdk/py && uv sync --all-extras -q && uv run pytest tests/test_id_contract.py
	@if command -v php >/dev/null 2>&1 && command -v composer >/dev/null 2>&1; then \
		echo "==> typeid-v1 parity: PHP"; \
		cd sdk/experimental/php && composer install --no-progress --quiet && vendor/bin/phpunit tests/Id/ContractTest.php; \
	else \
		echo "==> typeid-v1 parity: PHP toolchain not present, skipping (experimental SDK)"; \
	fi

lint: lint-go lint-ts lint-py lint-docs lint-config lint-links lint-sdk-paths ## Run all linters

lint-go: tools-golangci-lint ## Go: golangci-lint (pinned via GOLANGCI_LINT_VERSION)
	@GOFLAGS=-buildvcs=false $(GOLANGCI_LINT) run ./...
	@find go cmd contracts engine examples incubator -name "go.mod" -execdir env GOFLAGS=-buildvcs=false $(GOLANGCI_LINT) run ./... \;

lint-ts: ## TypeScript: eslint
	cd sdk/ts && pnpm eslint src/

lint-py: ## Python: ruff check + format
	cd sdk/py && uv run ruff check . && uv run ruff format --check .

lint-rs: ## Rust: cargo fmt --check + clippy (all features)
	cd sdk/experimental/rs && cargo fmt --all -- --check
	cd sdk/experimental/rs && cargo clippy --all-features --all-targets -- -D warnings

lint-docs: ## Markdown: markdownlint
	npx markdownlint-cli2 "README.md" "CHANGELOG.md" "RELEASING.md" "AGENTS.md" "docs/**/*.md" "cmd/kit/README.md" "incubator/**/*.md" --config examples/spaced/.markdownlint.yaml

lint-config: ## Validate configuration files and check for broken paths
	@echo "Validating configuration files..."
	@# Check all JSON files for syntax errors
	@find . -name "*.json" -not -path "*/node_modules/*" -not -path "*/vendor/*" -exec jq . {} + > /dev/null
	@# Check release-please for broken paths
	@for p in $$(jq -r '.packages | keys[]' .github/release-please-config.json); do \
		if [ "$$p" != "." ] && [ ! -d "$$p" ]; then \
			echo "Error: release-please-config.json references non-existent path: $$p"; \
			exit 1; \
		fi; \
	done
	@# Check pnpm-workspace.yaml for broken paths
	@for p in $$(jq -r '.packages[]' pnpm-workspace.yaml 2>/dev/null || yq -r '.packages[]' pnpm-workspace.yaml 2>/dev/null || grep -E '^- ' pnpm-workspace.yaml | sed 's/^- //'); do \
		if [ ! -d "$$p" ]; then \
			echo "Error: pnpm-workspace.yaml references non-existent path: $$p"; \
			exit 1; \
		fi; \
	done
	@echo "Config validation passed."

lint-links: ## Check for broken links in documentation
	lychee --config lychee.toml --offline docs/ README.md

lint-sdk-paths: ## Guard against repeated-sdk-segment path corruption recurrence
	@echo "Scanning for repeated-sdk-segment path corruption..."
	@! grep -IrEn 'sdk/sdk/sdk' . \
		--exclude-dir=.git \
		--exclude-dir=node_modules \
		--exclude-dir=.xray \
		--exclude-dir=.tlc \
		--exclude-dir=dist \
		--exclude-dir=bin \
		--exclude-dir=.venv \
		--exclude='*.pyc' \
		--exclude='*.db' \
		--exclude='docs-review-*.md' \
		--exclude='2026-03-28-kit-foundation.md' \
		--exclude='Makefile' \
		--exclude='.xray_*.md'
	@echo "No repeated-sdk-segment corruption detected."

proto: ## Generate protobuf + Connect/gRPC stubs
# Generated files are committed for go-get compatibility.
# Re-run after changing .proto files.
	cd contracts/proto/routellm/v1 && buf generate
	cd contracts/proto/crud/v1 && buf generate

openapi: ## Print OpenAPI extraction instructions (requires running server)
	@echo "Start server, then: curl http://localhost:8080/openapi.json > openapi.json"

clients: clients-ts clients-php clients-rs ## Build all polyglot clients

clients-ts: ## Build TypeScript client
	cd sdk/ts && pnpm install && pnpm build

clients-php: ## Install PHP client dependencies
	cd sdk/experimental/php && composer install

clients-rs: ## Build Rust client
	cd sdk/experimental/rs && cargo build --features api

clients-test: ## Test all polyglot clients
	cd sdk/ts && pnpm test
	cd sdk/experimental/php && composer test
	cd sdk/experimental/rs && cargo test --features api

api: proto clients ## Generate protos + build all clients

build: preflight builtins-sync ## Build the kit binary (re-syncs built-in templates first)
	@mkdir -p bin
	go build -buildvcs=false -o bin/kit ./cmd/kit

builtins-sync: ## Sync templates/cli-{go,ts,py,php,rs,shared} into internal/template/builtins/ for embedding
	@rm -rf internal/template/builtins
	@mkdir -p internal/template/builtins
	@for tmpl in cli-go cli-ts cli-py cli-php cli-rs shared; do \
		if [ -d templates/$$tmpl ]; then \
			cp -R templates/$$tmpl internal/template/builtins/$$tmpl; \
		fi; \
	done
	@# go.mod inside an embedded subtree creates a nested module that Go's
	@# embed refuses to cross; .go files inside the host module path get
	@# compiled by `go build ./...` and break on template placeholders.
	@# Rename both to .tmpl so the embed glob includes them; engine
	@# renders them back to their original names at output time.
	@find internal/template/builtins -name go.mod -exec sh -c 'mv "$$1" "$$1.tmpl"' _ {} \;
	@find internal/template/builtins -name "*.go" -exec sh -c 'mv "$$1" "$$1.tmpl"' _ {} \;
	@echo "synced built-in templates"

check-mirror-sync: ## Verify templates/ and internal/template/builtins/ are in sync
	@if diff -rq templates/ internal/template/builtins/ | grep -v '^Only in templates: ' | grep -q .; then \
		echo "Mirror drift detected between templates/ and internal/template/builtins/:"; \
		diff -rq templates/ internal/template/builtins/ | grep -v '^Only in templates: '; \
		echo ""; \
		echo "Fix: copy diverged files from templates/ to internal/template/builtins/ (source is canonical),"; \
		echo "or run: make builtins-sync"; \
		exit 1; \
	fi
	@echo "Mirror in sync."

test-templates: ## Run bats tests for template scripts
	bats templates/tests/lib.bats templates/tests/conform.bats

lint-templates: ## Run shellcheck lint via bats
	bats templates/tests/lint.bats

test-workflow: ## Run bats unit tests for cli-demo-media workflow shell logic
	bats .github/tests/cli-demo-media.bats

test-hook: ## Run bats tests for pre-push hook
	bats .github/tests/pre-push-hook.bats

job-test:
	go test ./go/runtime/job/... -count=1

job-integration-hatchet:
	docker compose -f go/runtime/job/hatchet/testdata/docker-compose.yml up -d --wait --wait-timeout 60 || \
		(docker compose -f go/runtime/job/hatchet/testdata/docker-compose.yml down -v; exit 1)
	go test -tags hatchet ./go/runtime/job/hatchet/... -count=1 -timeout 120s || \
		(docker compose -f go/runtime/job/hatchet/testdata/docker-compose.yml down -v; exit 1)
	docker compose -f go/runtime/job/hatchet/testdata/docker-compose.yml down -v

job-integration-restate:
	docker compose -f go/runtime/job/restate/testdata/docker-compose.yml up -d --wait --wait-timeout 60 || \
		(docker compose -f go/runtime/job/restate/testdata/docker-compose.yml down -v; exit 1)
	go test -tags restate ./go/runtime/job/restate/... -count=1 -timeout 120s || \
		(docker compose -f go/runtime/job/restate/testdata/docker-compose.yml down -v; exit 1)
	docker compose -f go/runtime/job/restate/testdata/docker-compose.yml down -v

job-integration-temporal:
	docker compose -f go/runtime/job/temporal/testdata/docker-compose.yml up -d --wait --wait-timeout 60 || \
		(docker compose -f go/runtime/job/temporal/testdata/docker-compose.yml down -v; exit 1)
	go test -tags temporal ./go/runtime/job/temporal/... -count=1 -timeout 120s || \
		(docker compose -f go/runtime/job/temporal/testdata/docker-compose.yml down -v; exit 1)
	docker compose -f go/runtime/job/temporal/testdata/docker-compose.yml down -v

test-release: ## Run e2e tests for release scripts
	bash scripts/test-release-e2e.sh

promote: ## Interactive release promotion
	@./scripts/promote-release.sh

promote-alpha: ## Promote main to alpha
	./scripts/promote-release.sh alpha

promote-beta: ## Promote alpha to beta
	./scripts/promote-release.sh beta

promote-rc: ## Promote beta to rc
	./scripts/promote-release.sh rc

promote-release: ## Promote rc to stable release
	./scripts/promote-release.sh release

refresh-secret-rules: ## Re-vendor gitleaks rules from latest tagged release
	@echo "Fetching latest gitleaks release tag..."
	@gh release view --repo gitleaks/gitleaks --json tagName -q .tagName > /tmp/gitleaks-tag
	@echo "Tag: $$(cat /tmp/gitleaks-tag)"
	@go run ./tools/vendor-gitleaks \
		--tag $$(cat /tmp/gitleaks-tag) \
		--out go/core/scope/rules/
	@echo "Done. Review diff, run tests, commit."

refresh-pii-rules: ## Re-vendor Presidio PII rules from latest tagged release
	@echo "Fetching latest Presidio release tag..."
	@gh release view --repo microsoft/presidio --json tagName -q .tagName > /tmp/presidio-tag
	@echo "Tag: $$(cat /tmp/presidio-tag)"
	@go run ./tools/vendor-presidio \
		--tag $$(cat /tmp/presidio-tag) \
		--out go/core/redact/rules/
	@echo "Done. Review diff, run tests, commit."

refresh-rules: refresh-secret-rules refresh-pii-rules ## Refresh all vendored rule corpora

sync-managed-assets: ## Re-copy templates/shared/*.{sh,toml} into cmd/kit/init/managed_assets/ (kit init embed)
	@echo "Syncing managed-block emitters into cmd/kit/init/managed_assets/..."
	@cp templates/shared/managed-block.sh \
	    templates/shared/emit-mise.sh \
	    templates/shared/emit-devcontainer-json.sh \
	    templates/shared/emit-docker-compose.sh \
	    templates/shared/emit-env-example.sh \
	    templates/shared/tool-versions.toml \
	    cmd/kit/init/managed_assets/
	@if [ -f templates/shared/apply-services.sh ]; then \
	    cp templates/shared/apply-services.sh cmd/kit/init/managed_assets/ ; \
	    echo "  + apply-services.sh (T-0808)"; \
	fi
	@if [ -d templates/shared/services ]; then \
	    mkdir -p cmd/kit/init/managed_assets/services/env ; \
	    cp templates/shared/services/*.yml cmd/kit/init/managed_assets/services/ ; \
	    cp templates/shared/services/env/*.env cmd/kit/init/managed_assets/services/env/ ; \
	    echo "  + services/ + services/env/ (T-0808 catalog)"; \
	fi
	@echo "Done. Review diff, rebuild kit, commit."
