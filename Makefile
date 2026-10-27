.PHONY: api audit-php build builtins-sync check check-go-version check-mirror-sync check-template-sources clients \
	clients-php clients-rs clients-test clients-ts job-integration-hatchet job-integration-restate \
	job-integration-temporal job-test lint lint-config lint-docs lint-go lint-links lint-lock-php \
	check-toolchain-parity lint-actions-pinned lint-adr-refs lint-internal-refs lint-lock-py lint-php lint-py lint-readmes lint-rs lint-sdk-paths lint-templates lint-ts openapi \
	preflight promote promote-beta promote-rc promote-open-line proto proto-check refresh-pii-rules \
	refresh-rules refresh-secret-rules setup test test-go test-go-integration test-go-race test-hook test-lint-scripts \
	completion-regen test-parity test-parity-breaker test-parity-bus test-parity-bus-network test-parity-completion test-parity-completion-spec test-parity-config test-parity-consent test-parity-kv test-parity-llm test-parity-mcp test-parity-netpolicy test-parity-redact test-parity-retry test-parity-statemachine test-parity-taxonomy test-parity-typeid test-parity-upgrade test-py \
	test-release test-rs test-rs-wasm test-templates test-ts \
	test-affected test-workflow tools tools-golangci-lint tools-govulncheck tools-osv-scanner \
	vuln vuln-go vuln-go-binary vuln-osv

# Tool versions — the mise config (mise.toml, plus .config/mise.toml for
# tools only this repo's CI uses) is the single source of truth for local
# and the repo's own CI; scripts/toolchain-pins.sh reads it. CI jobs feed the
# same pins to their setup actions, and `make check-toolchain-parity`
# fails when a file that carries its own copy (go.mod,
# rust-toolchain.toml, a workflow literal) disagrees.
#
# golangci-lint is installed by `tools-golangci-lint` rather than taken
# from mise so contributors without mise lint with the same release;
# `.github/workflows/ci.yml` reaches it through `make lint-go`.
#
# Note: `.github/workflows/lint.yml` is a reusable workflow exposed
# to OTHER hop-top repos via `workflow_call` and takes its own
# `inputs.version` independently — it is NOT covered by this pin.
TOOLCHAIN_PINS := $(CURDIR)/scripts/toolchain-pins.sh
ifndef GOLANGCI_LINT_VERSION
GOLANGCI_LINT_VERSION := v$(shell $(TOOLCHAIN_PINS) golangci-lint)
endif
MARKDOWNLINT_CLI2_VERSION := $(shell $(TOOLCHAIN_PINS) markdownlint-cli2)

# Go toolchain pin — the `go` entry in mise.toml. It equals go.mod's
# `go` directive (check-toolchain-parity enforces it), which is what
# setup-go installs in CI. This gate compares only the MINOR: that is
# the granularity export data breaks at (below), and a contributor on a
# newer patch lints correctly; mise and CI both run the exact pin.
#
# Why this pin needs its own gate: golangci-lint reads Go EXPORT DATA, whose
# format version is tied to the compiler that produced it. $(GOLANGCI_LINT_VERSION)
# tops out at export data version 2; a Go newer than the pin emits version 4
# and every package fails to typecheck. The resulting diagnostic names an
# arbitrary file that merely happens to import "bytes" first, so without this
# gate a toolchain mismatch presents as a lint failure in unrelated code.
GO_VERSION_PIN := $(shell $(TOOLCHAIN_PINS) go 2>/dev/null)
GO_VERSION_PIN_MINOR := $(shell echo '$(GO_VERSION_PIN)' | awk -F. '{ print $$1"."$$2 }')

# lint-go invokes the binary from $(LOCAL_BIN) directly. The
# `tools-golangci-lint` target is a hard dep, so the binary is
# guaranteed present by the time the recipe runs. Using an
# absolute path avoids PATH manipulation in `find -execdir`
# subshells (which would otherwise fall back to the PATH-installed
# version, defeating the version pin).
LOCAL_BIN := $(CURDIR)/bin
GOLANGCI_LINT := $(LOCAL_BIN)/golangci-lint

# Vulnerability scanners. Their pins live in the `env:` block of
# .github/workflows/vulnerability-scan.yml (govulncheck has no mise registry
# name; that workflow's header says why they stay out of mise.toml) and are
# read from there, so a bump in the workflow is a bump here too.
VULN_WORKFLOW := $(CURDIR)/.github/workflows/vulnerability-scan.yml
ifndef GOVULNCHECK_VERSION
GOVULNCHECK_VERSION := $(shell sed -n 's/^  GOVULNCHECK_VERSION: *//p' $(VULN_WORKFLOW))
endif
ifndef OSV_SCANNER_VERSION
OSV_SCANNER_VERSION := $(shell sed -n 's/^  OSV_SCANNER_VERSION: *//p' $(VULN_WORKFLOW))
endif
GOVULNCHECK := $(LOCAL_BIN)/govulncheck
OSV_SCANNER := $(LOCAL_BIN)/osv-scanner

preflight: ## Verify host toolchain matches the repo's declared minimum reqs
	@scripts/preflight.sh

check-go-version: ## Fail fast when the active Go minor differs from mise.toml's pin
	@if [ -z "$(GO_VERSION_PIN)" ]; then \
		echo "ERROR: no Go pin found in mise.toml." >&2; \
		echo "  add go = \"<go.mod's go directive>\" under [tools], then run" >&2; \
		echo "  make check-toolchain-parity" >&2; \
		exit 1; \
	fi
	@have=$$(go version 2>/dev/null | awk '{ print $$3 }' | sed 's/^go//'); \
	if [ -z "$$have" ]; then \
		echo "ERROR: no 'go' on PATH (mise.toml pins go $(GO_VERSION_PIN))." >&2; \
		echo "  fix: mise install    # or install Go $(GO_VERSION_PIN) by hand" >&2; \
		exit 1; \
	fi; \
	have_mm=$$(echo "$$have" | awk -F. '{ print $$1"."$$2 }'); \
	if [ "$$have_mm" != "$(GO_VERSION_PIN_MINOR)" ]; then \
		echo "ERROR: Go toolchain does not match the repo pin." >&2; \
		echo "  active : go $$have  ($$(command -v go))" >&2; \
		echo "  pinned : go $(GO_VERSION_PIN)  (mise.toml)" >&2; \
		echo "" >&2; \
		echo "  golangci-lint $(GOLANGCI_LINT_VERSION) cannot read export data from a" >&2; \
		echo "  newer Go; it would report typecheck failures in files you never" >&2; \
		echo "  touched. Refusing to lint against the wrong toolchain." >&2; \
		echo "" >&2; \
		echo "  fix: mise install && eval \"\$$(mise env -s bash)\"" >&2; \
		echo "       (or run any make target through: mise exec -- make <target>)" >&2; \
		exit 1; \
	fi; \
	echo "==> go $$have matches pin $(GO_VERSION_PIN_MINOR) (mise.toml and CI: $(GO_VERSION_PIN))"

tools: tools-golangci-lint tools-govulncheck tools-osv-scanner ## Install pinned dev tools into bin/

tools-golangci-lint: check-go-version ## Install the pinned golangci-lint version into bin/
	@mkdir -p $(LOCAL_BIN)
	@if [ ! -x $(LOCAL_BIN)/golangci-lint ] || ! $(LOCAL_BIN)/golangci-lint version 2>/dev/null | grep -q "$(GOLANGCI_LINT_VERSION:v%=%)"; then \
		echo "==> Installing golangci-lint $(GOLANGCI_LINT_VERSION) into $(LOCAL_BIN)"; \
		GOBIN=$(LOCAL_BIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	else \
		echo "==> golangci-lint $(GOLANGCI_LINT_VERSION) already installed"; \
	fi

# govulncheck and osv-scanner print no module version, so a stamp file next
# to each binary records the pin it was built from; a changed pin reinstalls.
tools-govulncheck: ## Install the pinned govulncheck version into bin/
	@mkdir -p $(LOCAL_BIN)
	@if [ -z "$(GOVULNCHECK_VERSION)" ]; then echo "ERROR: no GOVULNCHECK_VERSION in $(VULN_WORKFLOW)" >&2; exit 1; fi
	@if [ ! -x $(GOVULNCHECK) ] || [ "$$(cat $(GOVULNCHECK).version 2>/dev/null)" != "$(GOVULNCHECK_VERSION)" ]; then \
		echo "==> Installing govulncheck $(GOVULNCHECK_VERSION) into $(LOCAL_BIN)"; \
		GOBIN=$(LOCAL_BIN) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) \
			&& echo "$(GOVULNCHECK_VERSION)" > $(GOVULNCHECK).version; \
	else \
		echo "==> govulncheck $(GOVULNCHECK_VERSION) already installed"; \
	fi

tools-osv-scanner: ## Install the pinned osv-scanner version into bin/
	@mkdir -p $(LOCAL_BIN)
	@if [ -z "$(OSV_SCANNER_VERSION)" ]; then echo "ERROR: no OSV_SCANNER_VERSION in $(VULN_WORKFLOW)" >&2; exit 1; fi
	@if [ ! -x $(OSV_SCANNER) ] || [ "$$(cat $(OSV_SCANNER).version 2>/dev/null)" != "$(OSV_SCANNER_VERSION)" ]; then \
		echo "==> Installing osv-scanner $(OSV_SCANNER_VERSION) into $(LOCAL_BIN)"; \
		GOBIN=$(LOCAL_BIN) go install github.com/google/osv-scanner/v2/cmd/osv-scanner@$(OSV_SCANNER_VERSION) \
			&& echo "$(OSV_SCANNER_VERSION)" > $(OSV_SCANNER).version; \
	else \
		echo "==> osv-scanner $(OSV_SCANNER_VERSION) already installed"; \
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

test-affected: ## Tests for the languages this branch actually touched (BASE=<ref>)
	@BASE="$(BASE)" scripts/test-affected.sh

test-go: ## Go tests (skips long-running container tests)
	@go test -short ./... -count=1 -timeout 1200s
	@find go cmd contracts engine examples incubator -name "go.mod" -execdir go test -short ./... -count=1 -timeout 1200s \;

# PROPERTY_ITERATIONS pins the iteration count of the engine/store
# property tests (engine/store/property_iterations_test.go). Empty keeps
# each test's full count; ci.yml sets 100 on pull requests and leaves
# pushes and the nightly run at full. It rides its own variable rather
# than -short because -short also skips the testcontainer and kit-serve
# suites this target exists to run.
PROPERTY_ITERATIONS ?=

test-go-integration: export KIT_PROPERTY_ITERATIONS = $(PROPERTY_ITERATIONS)
test-go-integration: ## Go tests including testcontainer integration
	@go test ./... -count=1 -timeout 1200s
	@find go cmd contracts engine examples incubator -name "go.mod" -execdir go test ./... -count=1 -timeout 1200s \;

# Race detector over the library tree. The serve work landed tests that
# only fail under -race — root-factory parallel invocation in
# go/console/cli, per-invocation flag isolation in
# go/transport/cmdsurface, the transport services, and the concurrency
# guards under go/runtime, go/core, go/ai and go/storage. A plain
# `go test` runs every one of them green with the guard removed, so
# without this target a concurrency regression reaches the default
# branch unnoticed.
#
# Scope is the whole of go/ rather than a curated package list: a list
# goes stale the first time someone adds a concurrent test somewhere
# nobody thought to enumerate, and the failure mode is silent. Whole-repo
# ./... was measured and rejected — it pulls in sdk, examples, engine,
# incubator and contracts, whose testcontainer and cross-language suites
# dominate the run and carry no concurrency guards. Cold, with an empty
# build cache, go/ takes ~2m40s under -race; ./... did not finish inside
# 25 minutes.
#
# -buildvcs=false keeps the build off the git index: the VCS stamp
# fails ("error obtaining VCS status") when another checkout or hook
# holds it, which is routine on a dev machine running this alongside a
# push.
#
# `go list` runs on its own line, and its status is checked, because a
# PARTIAL failure is silent otherwise: go list prints the packages it
# resolved, fails on one (a broken build constraint, a malformed file, a
# new nested module) and exits non-zero, but inside a command
# substitution feeding a pipe that status is discarded — the recipe then
# races the survivors and reports green. A gate that goes green by
# skipping the package that broke is worse than no gate.
#
# The empty-list check is the same failure in a different disguise:
# `go test` with no package arguments prints "no Go files ... [setup
# failed]" and still exits 0, so an empty list would also report a
# green gate having tested nothing.
#
# Nothing is excluded. go/core/projects used to be, because
# projects.Write -> xdg.ConfigDir tripped the detector on the
# package-level globals github.com/adrg/xdg rewrites on every resolve;
# go/core/xdg now serializes those resolves, so the gate covers the
# whole of go/. Keep it that way — reach for a fix in the racing
# package before reaching for an exclusion here.
test-go-race: ## Go tests under the race detector (go/ tree, concurrency guards)
	@pkgs=$$(go list -buildvcs=false ./go/...) || { \
		echo "go list failed; refusing to run a partial race gate"; \
		exit 1; \
	}; \
	if [ -z "$$pkgs" ]; then \
		echo "no packages to race-test; refusing to report a green gate"; \
		exit 1; \
	fi; \
	go test -race -buildvcs=false -count=1 -timeout 1200s $$pkgs

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

# The bus on wasm32-unknown-unknown, run under Node: one thread, so the
# async pool is uncapped and close reports NotDrained instead of
# blocking. wasm-bindgen-test-runner comes from wasm-bindgen-cli, whose
# version must equal the wasm-bindgen in Cargo.lock.
test-rs-wasm: ## Rust: bus, domain and breaker wasm32 smoke tests under Node (needs the matching wasm-bindgen-cli)
	@want=$$(awk '/^name = "wasm-bindgen"$$/ { getline; gsub(/version = |"/, ""); print }' sdk/experimental/rs/Cargo.lock); \
	have=$$(wasm-bindgen --version 2>/dev/null | awk '{ print $$2 }'); \
	if [ "$$want" != "$$have" ]; then \
		echo "wasm-bindgen-cli '$$have' does not match Cargo.lock wasm-bindgen $$want:"; \
		echo "  cargo install wasm-bindgen-cli --version $$want --locked"; \
		exit 1; \
	fi
	cd sdk/experimental/rs && CARGO_TARGET_WASM32_UNKNOWN_UNKNOWN_RUNNER=wasm-bindgen-test-runner \
		cargo test --locked --target wasm32-unknown-unknown --no-default-features --features bus --test bus_wasm
	cd sdk/experimental/rs && CARGO_TARGET_WASM32_UNKNOWN_UNKNOWN_RUNNER=wasm-bindgen-test-runner \
		cargo test --locked --target wasm32-unknown-unknown --no-default-features --features domain --test domain_statemachine_wasm
	cd sdk/experimental/rs && CARGO_TARGET_WASM32_UNKNOWN_UNKNOWN_RUNNER=wasm-bindgen-test-runner \
		cargo test --locked --target wasm32-unknown-unknown --no-default-features --features breaker --test breaker_wasm

test-parity: test-parity-typeid test-parity-consent test-parity-breaker test-parity-bus test-parity-bus-network test-parity-config test-parity-retry test-parity-completion test-parity-completion-spec test-parity-netpolicy test-parity-llm test-parity-kv test-parity-mcp test-parity-taxonomy test-parity-statemachine test-parity-redact test-parity-upgrade ## Cross-language parity tests
	go test -tags parity ./go/console/cli/... -timeout 300s -count=1
	cd engine/sdk/py-kit-engine && uv sync --all-extras -q
	go test -tags parity ./engine/sdk/parity/... -timeout 300s -count=1

# Per-SDK loaders for contracts/typeid-v1/fixtures.json. Each SDK runs
# its own contract test; a wire-incompatible change in any SDK (or in
# the shared fixture file) fails this target. PHP is treated as
# optional because the kit's PHP toolchain is experimental and not
# every CI runner ships it — when `php` and `composer` are present we
# run the PHP contract test too.
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

# MCP wire conformance over sdk/tests/cross-lang/fixtures/mcp-wire.json.
# The fixture is GENERATED from the Go surface (see
# surface_mcp_fixtures_gen_test.go), so Go's own gate is the drift check
# rather than a replay: `go test ./go/transport/cmdsurface/` fails if the
# surface no longer produces the committed bytes. The four SDKs replay it.
#
# Two sections, and a runner must honour both. `cases` each get a FRESH
# mount, so no case observes another's state. `sequences` are ordered
# steps against ONE long-lived mount — that is where a port that caches
# its leaf set, or attaches lazy flags non-idempotently, is caught. Both
# defects pass every case; only the sequence sees them.
#
# PHP is optional for the same reason as test-parity-typeid: the PHP
# toolchain is experimental and not every runner ships it.
test-parity-mcp: ## MCP dual-spec wire conformance across Go + 4 SDKs
	@echo "==> mcp-wire parity: Go (fixture drift gate)"
	go test ./go/transport/cmdsurface/ -run '^TestGenerateMCPWireFixtures$$' -count=1 -timeout 120s
	@echo "==> mcp-wire parity: Rust"
	cd sdk/experimental/rs && cargo test --features mcp --test mcp_wire_conformance --locked
	@echo "==> mcp-wire parity: TypeScript"
	cd sdk/ts && pnpm vitest run src/mcp/conformance.test.ts
	@echo "==> mcp-wire parity: Python"
	cd sdk/py && uv sync --all-extras -q && uv run pytest tests/test_mcp_conformance.py
	@if command -v php >/dev/null 2>&1 && command -v composer >/dev/null 2>&1; then \
		echo "==> mcp-wire parity: PHP"; \
		cd sdk/experimental/php && composer install --no-progress --quiet && vendor/bin/phpunit tests/Mcp/WireConformanceTest.php; \
	else \
		echo "==> mcp-wire parity: PHP toolchain not present, skipping (experimental SDK)"; \
	fi

# Exit-code taxonomy gate over contracts/exit-taxonomy-v1/taxonomy.json.
#
# Go's entry is a DRIFT GATE, not a replay: the contract is generated
# from go/console/output/envelope, so Go's job is to prove the
# checked-in file still matches what the live taxonomy emits. The four
# SDKs load the file and check their own class/exit/transience table
# against it.
#
# That asymmetry is the whole point. A class added to Go turns the Go
# gate red until the contract is regenerated, and regenerating then
# turns all four SDK loaders red until they carry the class too — so a
# taxonomy change cannot land in one language and reach none of the
# others, which is how CONSENT_REFUSED 7 and PREREQUISITE 70 shipped in
# Go and were missing from all four ports for two releases.
#
# PHP is optional for the same reason as test-parity-typeid: the PHP
# toolchain is experimental and not every runner ships it.
test-parity-taxonomy: ## exit-code taxonomy across Go + 4 SDKs
	@echo "==> exit-taxonomy parity: Go (contract drift gate)"
	go test ./go/console/output/envelope/ -run '^TestGenerateExitTaxonomyContract$$|^TestExitTaxonomyContractIsSelfConsistent$$' -count=1 -timeout 60s
	@echo "==> exit-taxonomy parity: Rust"
	cd sdk/experimental/rs && cargo test --features output --test taxonomy_contract --locked
	@echo "==> exit-taxonomy parity: TypeScript"
	cd sdk/ts && pnpm vitest run test/output/taxonomy-contract.test.ts
	@echo "==> exit-taxonomy parity: Python"
	cd sdk/py && uv sync --all-extras -q && uv run pytest tests/test_taxonomy_contract.py
	@if command -v php >/dev/null 2>&1 && command -v composer >/dev/null 2>&1; then \
		echo "==> exit-taxonomy parity: PHP"; \
		cd sdk/experimental/php && composer install --no-progress --quiet && vendor/bin/phpunit tests/Output/TaxonomyContractTest.php; \
	else \
		echo "==> exit-taxonomy parity: PHP toolchain not present, skipping (experimental SDK)"; \
	fi

# Shared bus conformance corpus: the Go and Rust runners read the same
# vectors under contracts/bus-v1.
test-parity-bus: ## bus-v1 conformance corpus, CloudEvents, streams and sinks included (Go and Rust runners)
	@echo "==> bus-v1 conformance: Go"
	go test ./go/runtime/bus/ -run '^(TestConformance|TestCloudEventsConformance|TestStreamConformance|TestSinkConformance)' -count=1
	@echo "==> bus-v1 conformance: Rust"
	cd sdk/experimental/rs && cargo test --test bus_conformance --test bus_cloudevents_conformance --test 'bus_stream_conformance*' --test bus_sinks_conformance --all-features --locked

# Bus network across languages: Go hub <-> Rust client, Rust hub <-> Go
# client and mixed three-node relays, with and without auth. The Go peer
# is sdk/tests/cross-lang/bus-network/gonode; BUS_NETWORK_CROSSLANG gates
# the cases so a plain `cargo test` needs no Go toolchain.
test-parity-bus-network: ## bus network cross-language e2e (Go <-> Rust hubs, clients, mixed relay)
	@echo "==> bus network: Go <-> Rust over WebSocket"
	cd sdk/experimental/rs && BUS_NETWORK_CROSSLANG=1 cargo test --locked --features bus-network --test bus_network_crosslang -- --test-threads=1

# Completion-spec exports (Fig module, Usage KDL) over one fixture tree.
# Go runner only; a port adds its runner here.
test-parity-completion-spec: ## completion-spec-v1 corpus: Fig and Usage exports, byte for byte (Go and Rust runners)
	@echo "==> completion-spec-v1 conformance: Go"
	go test ./go/console/cli/completionspec/ -run '^TestConformanceCompletionSpec' -count=1
	@echo "==> completion-spec-v1 conformance: Rust"
	cd sdk/experimental/rs && cargo test --features cli --test cli_completion_spec_conformance --locked

# Shared retry-v1 backoff corpus: Go and Rust runners read the same vectors.
test-parity-config: ## config-v1 conformance corpus (Go and Rust runners)
	@echo "==> config-v1 conformance: Go"
	go test ./go/core/config/ -run '^TestConformance' -count=1
	@echo "==> config-v1 conformance: Rust"
	cd sdk/experimental/rs && cargo test --features config --test config_conformance --locked

test-parity-redact: ## redact-v1 conformance corpus: rules dump, spans and output (Go and Rust runners)
	@echo "==> redact-v1 conformance: Go"
	go test ./go/core/redact/ -run '^TestConformanceRedact' -count=1
	@echo "==> redact-v1 conformance: Rust"
	cd sdk/experimental/rs && cargo test --features redact --test redact_conformance --locked

test-parity-retry: ## retry-v1 conformance corpus (Go and Rust runners)
	@echo "==> retry-v1 conformance: Go"
	go test ./go/core/util/ -run '^TestConformanceRetry' -count=1
	@echo "==> retry-v1 conformance: Rust"
	cd sdk/experimental/rs && cargo test --test retry_conformance --locked

# Breaker corpus: circuit, budgets, rate limiter, OnTrip, logs and bus
# events under a fake clock. Go's bus events publish on goroutines,
# hence -race.
test-parity-breaker: ## breaker-v1 conformance corpus (Go and Rust runners)
	@echo "==> breaker-v1 conformance: Go"
	go test -race ./go/core/breaker/ -run '^TestConformanceBreaker' -count=1
	@echo "==> breaker-v1 conformance: Rust"
	cd sdk/experimental/rs && cargo test --features breaker --test breaker_conformance --locked

# Per-capability consent corpus: on-disk grant records, decision and
# refusal semantics, audit records, delivery and the telemetry-consent
# migration. A port adds its runner here.
test-parity-consent: ## consent-v1 conformance corpus (Go and Rust runners)
	@echo "==> consent-v1 conformance: Go"
	go test ./go/core/consent/ -run '^TestConformanceConsent' -count=1
	@echo "==> consent-v1 conformance: Rust"
	cd sdk/experimental/rs && cargo test --locked --features consent-file,output --test consent_conformance --test consent_delivery_conformance

# Domain state machine corpus: transitions, error texts, the veto wrap,
# payload JSON bytes and topic options. Go's post-transition event runs
# on a goroutine, hence -race.
test-parity-statemachine: ## statemachine-v1 conformance corpus (Go and Rust runners)
	@echo "==> statemachine-v1 conformance: Go"
	go test -race ./go/runtime/domain/ -run '^TestConformanceStateMachine' -count=1
	@echo "==> statemachine-v1 conformance: Rust"
	cd sdk/experimental/rs && cargo test --features domain --test domain_statemachine_conformance --locked

# Versioned state migrations: version order, pending selection, and the
# runner's backup, apply, rollback and prune sequence on a recording driver.
test-parity-upgrade: ## upgrade-v1 conformance corpus (Go and Rust runners)
	@echo "==> upgrade-v1 conformance: Go"
	go test ./go/core/upgrade/ -run '^TestConformanceUpgrade' -count=1
	@echo "==> upgrade-v1 conformance: Rust"
	cd sdk/experimental/rs && cargo test --features upgrade --test upgrade_conformance --locked

# After a cobra bump: rewrite the completion-v1 script goldens and the
# Rust script templates from the cobra version in go.mod, then run
# test-parity-completion. Review the diff; both runners pin the result.
completion-regen: ## Regenerate completion-v1 script goldens and the Rust completion templates from cobra
	go test ./go/console/cli/completion/ -run '^(TestConformanceCompletionScripts|TestCompletionTemplatesInSync)$$' -count=1 -update

test-parity-completion: ## completion-v1 conformance corpus (Go and Rust runners)
	@echo "==> completion-v1 conformance: Go"
	go test -race ./go/console/cli/completion/ -run '^TestConformanceCompletion' -count=1
	@echo "==> completion-v1 conformance: Rust"
	cd sdk/experimental/rs && cargo test --features cli,config --test cli_completion_conformance --locked

test-parity-netpolicy: ## netpolicy-v1 conformance corpus (Go and Rust runners)
	@echo "==> netpolicy-v1 conformance: Go"
	go test ./go/core/netpolicy/ -run '^TestConformanceNetpolicy' -count=1
	@echo "==> netpolicy-v1 conformance: Rust"
	cd sdk/experimental/rs && cargo test --features api --test netpolicy_conformance --locked

# llm core and anthropic adapter corpus: URI parsing and redaction,
# credential endpoint guard, request bodies, headers, SSE tokens and
# accumulation, error mapping and retries. Both runners serve the
# vectors from loopback only.
test-parity-llm: ## llm-v1 conformance corpus (Go and Rust runners)
	@echo "==> llm-v1 conformance: Go"
	go test ./go/ai/llm/ ./go/ai/llm/anthropic/ -run '^TestConformance' -count=1
	@echo "==> llm-v1 conformance: Rust"
	cd sdk/experimental/rs && cargo test --features llm-anthropic --test llm_conformance --locked

# Cross-process kv storage-binding gate over contracts/kv-v1/keys.json.
# The Go test drives the Rust half as a `cargo test` subprocess, so this
# target needs BOTH toolchains — hence its home in test-parity rather
# than test-go. KV_CROSSLANG gates the cross-process cases so a plain
# `go test ./...` stays Rust-free; setting it here is what makes the
# gate live in CI.
test-parity-kv: ## kv-v1 cross-language storage-binding gate (Go <-> Rust)
	@echo "==> kv-v1 parity: Go <-> Rust cross-process"
	KV_CROSSLANG=1 go test ./go/storage/kv/sqlite/... -run '^TestCrossLang' -count=1 -timeout 300s -v

lint: check-toolchain-parity lint-go lint-ts lint-py lint-php lint-lock-py lint-lock-php audit-php lint-docs lint-readmes lint-config lint-links lint-sdk-paths lint-adr-refs lint-internal-refs lint-actions-pinned ## Run all linters

lint-go: check-go-version tools-golangci-lint ## Go: golangci-lint (pinned via GOLANGCI_LINT_VERSION + mise.toml Go pin)
	@GOFLAGS=-buildvcs=false $(GOLANGCI_LINT) run ./...
	@find go cmd contracts engine examples incubator -name "go.mod" -execdir env GOFLAGS=-buildvcs=false $(GOLANGCI_LINT) run ./... \;

lint-ts: ## TypeScript: eslint + tsc type-check (src and tests)
	cd sdk/ts && pnpm eslint src/
	cd sdk/ts && pnpm run typecheck
	cd engine/sdk/ts-kit-engine && pnpm run typecheck

lint-py: ## Python: ruff check + format
	cd sdk/py && uv run ruff check . && uv run ruff format --check .
	cd engine/sdk/py-kit-engine && uv run ruff check . && uv run ruff format --check .

# `uv sync` rewrites uv.lock in place when it disagrees with pyproject.toml,
# so drift never fails a build — it just lands as an unrelated dirty file in
# the next contributor's tree. --check resolves without writing and exits
# non-zero instead.
lint-lock-py: ## Python: uv.lock consistent with pyproject.toml
	cd sdk/py && uv lock --check
	cd engine/sdk/py-kit-engine && uv lock --check

lint-lock-php: ## PHP: composer.lock consistent with composer.json
	cd sdk/experimental/php && composer validate --check-lock --no-check-publish

# `--locked` audits composer.lock directly, so this runs without a vendor/
# tree and reports what CI would actually install. Severity gate: low is
# ignored, medium and up fail. A hard gate on every severity turns any new
# low advisory against a pinned transitive dep into a red build on unrelated
# PRs — which is how audit steps end up commented out. Abandoned packages
# report but don't fail: abandonment is not a vulnerability and rarely has a
# same-day replacement. `--ignore-unreachable` keeps a Packagist outage from
# failing the build for a reason unrelated to the code under test.
audit-php: ## PHP: composer.lock free of medium+ security advisories
	cd sdk/experimental/php && composer audit --locked --no-dev \
		--ignore-severity=low --abandoned=report --ignore-unreachable

# Local mirror of .github/workflows/vulnerability-scan.yml: same scanners,
# same pins, same scope, same verdicts; text output instead of the
# workflow's JSON/SARIF/Markdown artifacts. Every scan queries osv.dev or
# vuln.go.dev, and an advisory can land without a code change, so `vuln` is
# kept out of `check` and `lint`; CI gates it on every pull request and
# weekly. `make -k` runs every scan before `vuln` fails, like the workflow's
# independent jobs and fail-fast: false matrix.
vuln: ## Run every vulnerability scan CI runs (needs network)
	@$(MAKE) --no-print-directory -k vuln-osv vuln-go vuln-go-binary

# Lockfiles outside examples/ gate (exit 1 = findings); examples/ is
# report-only and fails only when the scanner itself fails (exit > 1).
# Ignores: osv-scanner.toml.
vuln-osv: tools-osv-scanner ## osv-scanner over every lockfile (examples/ report-only)
	@lock_rc=0; ex_rc=0; \
	echo "==> osv-scanner: lockfiles outside examples/ (gating)"; \
	$(OSV_SCANNER) scan source --config osv-scanner.toml --no-call-analysis go \
		-r --experimental-exclude examples . || lock_rc=$$?; \
	if [ -d examples ]; then \
		echo "==> osv-scanner: examples/ (report only)"; \
		$(OSV_SCANNER) scan source --config osv-scanner.toml --no-call-analysis go \
			--allow-no-lockfiles -r examples || ex_rc=$$?; \
	fi; \
	if [ "$$lock_rc" -eq 1 ]; then echo "ERROR: osv-scanner: advisories in lockfiles outside examples/" >&2; \
	elif [ "$$lock_rc" -ne 0 ]; then echo "ERROR: osv-scanner failed on lockfiles (exit $$lock_rc)" >&2; fi; \
	if [ "$$ex_rc" -gt 1 ]; then echo "ERROR: osv-scanner failed on examples/ (exit $$ex_rc)" >&2; fi; \
	[ "$$lock_rc" -eq 0 ] && [ "$$ex_rc" -le 1 ]

# Every tracked go.mod outside testdata/, as the workflow's go-modules job
# finds them. Fails (exit 3) only on a vulnerability the code calls.
vuln-go: check-go-version tools-govulncheck ## govulncheck source scan of every tracked Go module
	@rc=0; \
	for mod in $$(git ls-files -- 'go.mod' ':(glob)**/go.mod' | grep -v '/testdata/'); do \
		dir=$$(dirname "$$mod"); \
		echo "==> govulncheck source: $$dir/go.mod"; \
		$(GOVULNCHECK) -C "$$dir" -show verbose ./... || rc=1; \
	done; \
	if [ "$$rc" -ne 0 ]; then echo "ERROR: govulncheck: a module calls vulnerable code" >&2; fi; \
	exit $$rc

# A bare cli.Root built with the active toolchain (check-go-version keeps it
# on the repo pin, as setup-go does in CI) and scanned in binary mode.
vuln-go-binary: check-go-version tools-govulncheck ## govulncheck binary scan of a bare cli.Root (rootprobe)
	@tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	go build -buildvcs=false -o "$$tmp/rootprobe" ./go/console/cli/testdata/rootprobe && \
	echo "==> govulncheck binary: bare cli.Root ($$(go version))" && \
	$(GOVULNCHECK) -mode binary -show verbose "$$tmp/rootprobe"

# Two passes: src/ at level 5, tests/ at 2 (see phpstan.neon for why they
# differ). Both run via `composer analyse`, so this target and a bare
# `composer analyse` gate identically. Neither config carries a baseline or
# an ignore list, so every error reported is a real one. Needs vendor/
# (unlike audit-php), hence the install.
lint-php: ## PHP: phpstan static analysis (levels pinned in phpstan.neon)
	cd sdk/experimental/php && composer install --no-progress --quiet && composer analyse

lint-rs: ## Rust: cargo fmt --check + clippy (all features)
	cd sdk/experimental/rs && cargo fmt --all -- --check
	cd sdk/experimental/rs && cargo clippy --all-features --all-targets -- -D warnings

# release-please CHANGELOG.md files get their own config: their entries are
# commit text copied verbatim, so prose rules there are relaxed (see
# .markdownlint-changelog.yaml); structure is still checked.
lint-docs: ## Markdown: markdownlint (version pinned in mise.toml)
	npx --yes markdownlint-cli2@$(MARKDOWNLINT_CLI2_VERSION) "README.md" "RELEASING.md" "AGENTS.md" "docs/**/*.md" "cmd/kit/README.md" "incubator/**/*.md" "#incubator/**/CHANGELOG.md" --config examples/spaced/.markdownlint.yaml
	npx --yes markdownlint-cli2@$(MARKDOWNLINT_CLI2_VERSION) "CHANGELOG.md" "incubator/**/CHANGELOG.md" --config .markdownlint-changelog.yaml

lint-readmes: ## Markdown: folder README coverage, Contents links, shape caps
	scripts/lint-readmes

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

# Decision records are kept outside this repository, so an ADR number in
# a comment, doc, help string or workflow names a document no reader of
# the repo can open. Point at the in-repo doc that holds the content, or
# state the rationale inline. CHANGELOG.md files are excluded: they are
# generated from commit history and never hand-edited.
#
# git grep exits 1 on no match and >1 on error; only 1 passes, so a
# failed scan can never read as a clean one.
lint-adr-refs: ## Guard against ADR-number mentions (decision records live outside the repo)
	@echo "Scanning for ADR-number mentions..."
	@status=0; \
	git grep -nIE 'ADR[- ]?[0-9]{4}' -- . ':(exclude,glob)**/CHANGELOG.md' || status=$$?; \
	case $$status in \
		0) echo "error: ADR-number mentions found above; decision records are not in this repo." >&2; \
		   echo "       Point at the in-repo doc that holds the content, or state the rationale inline." >&2; \
		   exit 1 ;; \
		1) echo "No ADR-number mentions found." ;; \
		*) echo "error: git grep failed (exit $$status); scan did not run." >&2; exit $$status ;; \
	esac

# Sibling of lint-adr-refs for everything else a reader of the repo
# cannot open: a real home directory, a workspace-private doc, a design
# note, working note or tracker name, or a spec/contract section or
# decision number cited without naming its file. The patterns and the
# placeholder users allowed in example paths live in the script header.
lint-internal-refs: ## Guard against references to documents and paths outside the repo
	@scripts/lint-internal-refs.sh

# Every workflow and composite-action `uses:` is pinned to a full commit
# SHA, version in a trailing comment; Dependabot keeps them current.
# Second pass: the workflow templates adopters get (kit init, scaffold.sh,
# conform) and the example workflows; generated output is linted by
# TestBootstrap_GeneratedWorkflowsArePinned (cmd/kit/init).
ADOPTER_WORKFLOWS = 'templates/shared/ci/*.yml' 'templates/shared/ci/*.yml.tmpl' \
	'templates/ci/*/github-actions.yml' 'examples/*/.github/workflows/*.yml'

lint-actions-pinned: ## Fail on GitHub Actions `uses:` refs not pinned to a commit SHA
	@scripts/lint-actions-pinned.sh
	@scripts/lint-actions-pinned.sh $$(git ls-files -- $(ADOPTER_WORKFLOWS))

# mise.toml pins the toolchain; CI and the Makefile read it. The files
# that must carry their own copy (go.mod, rust-toolchain.toml, uv.lock's
# ruff, workflow literals, the devcontainer) are held to it here. The
# script header lists every check.
check-toolchain-parity: ## Fail when a declared tool version disagrees with mise.toml
	@scripts/check-toolchain-parity.sh

proto: ## Generate protobuf + Connect/gRPC stubs
# Generated files are committed for go-get compatibility.
# Re-run after changing .proto files.
	cd contracts/proto/routellm/v1 && buf generate
	cd contracts/proto/crud/v1 && buf generate
	cd contracts/proto/cmdsurface/v1 && buf generate

# Proto modules whose buf.gen.yaml pins every plugin version, so a
# regeneration is reproducible, and the paths their stubs land in
# (crud and routellm also generate inside their module directory).
PROTO_PINNED_DIRS := contracts/proto/cmdsurface/v1 contracts/proto/crud/v1 contracts/proto/routellm/v1
PROTO_PINNED_OUT  := go/transport/cmdsurface/gen contracts/proto/crud/v1 contracts/proto/routellm/v1/gen sdk/ts/src/gen

proto-check: ## Lint pinned protos, regenerate their stubs, fail on drift
	@for d in $(PROTO_PINNED_DIRS); do \
		(cd $$d && buf lint && buf format --diff --exit-code && buf generate) || exit 1; \
	done
	@if ! git diff --exit-code --stat -- $(PROTO_PINNED_DIRS) $(PROTO_PINNED_OUT) \
		|| [ -n "$$(git status --porcelain --untracked-files=all -- $(PROTO_PINNED_OUT))" ]; then \
		git status --short -- $(PROTO_PINNED_OUT); \
		echo "Generated protobuf stubs are stale: run 'make proto' and commit the result."; \
		exit 1; \
	fi
	@echo "Generated protobuf stubs match their .proto sources."

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

# Template trees mirrored into internal/template/builtins/ for embedding.
# templates/ is canonical; the mirror is a verbatim copy except for
# relative Markdown links that leave a template tree, which
# scripts/sync-builtins.sh rebases so they resolve from the mirror too.
BUILTIN_TEMPLATES := cli-go cli-ts cli-py cli-php cli-rs shared

builtins-sync: check-template-sources ## Sync templates/cli-{go,ts,py,php,rs,shared} into internal/template/builtins/ for embedding
	@scripts/sync-builtins.sh internal/template/builtins $(BUILTIN_TEMPLATES)
	@echo "synced built-in templates"

check-template-sources: ## Verify mirrored templates ship Go sources (*.go, go.mod) as *.tmpl
	@# go.mod inside an embedded subtree creates a nested module that Go's
	@# embed refuses to cross; .go files anywhere under the host module get
	@# compiled by `go build ./...` and break on template placeholders.
	@# Templates ship both as *.tmpl (the engine strips the suffix at render
	@# time) so the mirror stays a verbatim copy of templates/.
	@bad=$$(cd templates && find $(BUILTIN_TEMPLATES) -type f \( -name '*.go' -o -name go.mod \) 2>/dev/null); \
	if [ -n "$$bad" ]; then \
		echo "Go sources under templates/ must carry a .tmpl suffix (see internal/template/embed.go):"; \
		echo "$$bad" | sed 's|^|  templates/|'; \
		echo ""; \
		echo "Fix: git mv <file> <file>.tmpl (render strips the suffix)"; \
		exit 1; \
	fi

# Regenerates the mirror into a temp dir with the same script builtins-sync
# runs and diffs it against the committed one, so the check and the sync
# can never disagree about what "in sync" means.
check-mirror-sync: check-template-sources ## Verify internal/template/builtins/ is what builtins-sync produces from templates/
	@tmp=$$(mktemp -d) || exit 1; \
	trap 'rm -rf "$$tmp"' EXIT; \
	scripts/sync-builtins.sh "$$tmp/builtins" $(BUILTIN_TEMPLATES) || exit 1; \
	if ! diff -r "$$tmp/builtins" internal/template/builtins >"$$tmp/drift" 2>&1; then \
		echo "Mirror drift detected between templates/ and internal/template/builtins/:"; \
		sed -e "s|$$tmp/builtins|<builtins-sync output>|g" "$$tmp/drift"; \
		echo ""; \
		echo "Fix: edit templates/ (source is canonical), then run: make builtins-sync"; \
		exit 1; \
	fi
	@echo "Mirror in sync."

.PHONY: check-template-kit-pin
check-template-kit-pin: ## Fail when the cli-go template's kit pin lags proxy.golang.org by more than one release
	@go run ./internal/tools/check-kit-pin

test-templates: ## Run bats tests for template scripts
	bats templates/tests/lib.bats templates/tests/conform.bats

lint-templates: ## Run shellcheck lint via bats
	bats templates/tests/lint.bats

test-workflow: ## Run bats unit tests for workflow shell logic
	bats .github/tests/cli-demo-media.bats .github/tests/changelog-rewrite.bats \
		.github/tests/release-promote-gate.bats

test-hook: ## Run bats tests for pre-push hook
	bats .github/tests/pre-push-hook.bats

test-lint-scripts: ## Run bats tests for repo lint and sync scripts
	bats .github/tests/lint-internal-refs.bats .github/tests/sync-builtins.bats \
		.github/tests/toolchain-parity.bats .github/tests/lint-actions-pinned.bats

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

# Target-versioning steps (RELEASING.md), recorded in the release-please
# config by hop-top/.github's promote.js and committed on the current branch
# (push it to next). DRY_RUN=1 prints only; NO_COMMIT=1 writes, no commit.
#   make promote-beta PACKAGE='kit qmochi'     (paths or components)
#   make promote-rc GROUP=kit DRY_RUN=1
#   make promote-open-line LINE=1.0.0 GROUP=kit
#   make promote ARGS='clear channel --package kit'
PROMOTE_SELECT = $(foreach p,$(PACKAGE),--package $(p)) $(foreach g,$(GROUP),--group $(g)) \
	$(if $(DRY_RUN),--dry-run) $(if $(NO_COMMIT),--no-commit)

promote: ## Run promote.js with ARGS (channel | open-line | clear; see RELEASING.md)
	@scripts/promote-release.sh $(ARGS)

promote-beta promote-rc: ## Promote PACKAGE / GROUP to the next channel on its current line
	@scripts/promote-release.sh channel $(subst promote-,,$@) $(PROMOTE_SELECT)

promote-open-line: ## Open line LINE (x.y.z) for PACKAGE / GROUP
	@scripts/promote-release.sh open-line $(LINE) $(PROMOTE_SELECT)

refresh-secret-rules: ## Re-vendor gitleaks rules from latest tagged release
	@echo "Fetching latest gitleaks release tag..."
	@gh release view --repo gitleaks/gitleaks --json tagName -q .tagName > /tmp/gitleaks-tag
	@echo "Tag: $$(cat /tmp/gitleaks-tag)"
	@go run ./internal/tools/vendor-gitleaks \
		--tag $$(cat /tmp/gitleaks-tag) \
		--out go/core/scope/rules/
	@echo "Done. Review diff, run tests, commit."

refresh-pii-rules: ## Re-vendor Presidio PII rules from latest tagged release
	@echo "Fetching latest Presidio release tag..."
	@gh release view --repo microsoft/presidio --json tagName -q .tagName > /tmp/presidio-tag
	@echo "Tag: $$(cat /tmp/presidio-tag)"
	@go run ./internal/tools/vendor-presidio \
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
	    echo "  + apply-services.sh"; \
	fi
	@if [ -d templates/shared/services ]; then \
	    mkdir -p cmd/kit/init/managed_assets/services/env ; \
	    cp templates/shared/services/*.yml cmd/kit/init/managed_assets/services/ ; \
	    cp templates/shared/services/env/*.env cmd/kit/init/managed_assets/services/env/ ; \
	    echo "  + services/ + services/env/ (catalog)"; \
	fi
	@echo "Done. Review diff, rebuild kit, commit."
