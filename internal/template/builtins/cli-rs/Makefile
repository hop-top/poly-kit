.PHONY: build test lint format check setup \
       quality audit unused pkgcheck coverage dupes mutation \
       promote promote-alpha promote-beta promote-rc promote-release

# Line-coverage floor for `make coverage`: the scaffold ships at what
# its own starter tests reach. Raise it as tests land; never lower it
# to get a change through.
COVERAGE_MIN ?= 80

check: lint test

build:
	cargo build --release

test:
	cargo test --locked

lint:
	cargo fmt --all -- --check
	cargo clippy --all-targets -- -D warnings

format:
	cargo fmt --all

setup:
	cargo fetch

# quality runs the engineering gates beyond check: advisories and
# licenses, unused dependencies, the publishable crate, a coverage
# floor and copy-paste. CI runs it on every PR and weekly. Dead code
# needs no extra tool: clippy -D warnings already fails on it.
quality: audit unused pkgcheck coverage dupes

audit:
	cargo deny check

unused:
	cargo machete

# --allow-dirty: run before committing too. The packaged crate is
# still built from what `cargo package` would upload.
pkgcheck:
	cargo package --locked --allow-dirty

coverage:
	cargo llvm-cov --locked --fail-under-lines $(COVERAGE_MIN)

dupes:
	jscpd .

# mutation checks that the tests catch changed behaviour, not only
# that they run the code. Slow: CI runs it weekly, report-only.
mutation:
	cargo mutants

promote:
	@scripts/promote-release.sh

promote-alpha promote-beta promote-rc promote-release:
	@scripts/promote-release.sh $(subst promote-,,$@)
