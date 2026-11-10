.PHONY: build test lint format typecheck links check setup \
       quality audit unused pkgcheck coverage dupes mutation \
       promote promote-alpha promote-beta promote-rc \
       promote-release

# Line-coverage floor for `make coverage`: the scaffold ships at what
# its own starter tests reach. Raise it as tests land; never lower it
# to get a change through.
COVERAGE_MIN ?= 80

check: lint typecheck test links

build:
	uv build

test:
	uv run pytest

lint:
	uv run ruff check src/ tests/
	uv run ruff format --check src/ tests/

format:
	uv run ruff format src/ tests/

typecheck:
	uv run mypy src/

# quality runs the engineering gates beyond check: known
# vulnerabilities, unused code and dependencies, the built
# distribution, a coverage floor and copy-paste. CI runs it on every
# PR and weekly.
quality: audit unused pkgcheck coverage dupes

audit:
	uv run pip-audit --skip-editable

unused:
	uv run deptry src
	uv run vulture

pkgcheck:
	rm -rf dist
	uv build
	uv run twine check --strict dist/*

coverage:
	uv run pytest --cov --cov-fail-under=$(COVERAGE_MIN)

dupes:
	jscpd .

# mutation checks that the tests catch changed behaviour, not only
# that they run the code. Slow: CI runs it weekly, report-only.
mutation:
	uv run mutmut run

links:
	@if command -v lychee >/dev/null 2>&1; then \
		lychee --no-progress .; \
	else \
		echo "lychee not installed; skipping link check"; \
	fi

setup:
	uv sync --group dev
	@command -v lychee >/dev/null 2>&1 || cargo install lychee

promote:
	@scripts/promote-release.sh

promote-alpha promote-beta promote-rc promote-release:
	@scripts/promote-release.sh $(subst promote-,,$@)
