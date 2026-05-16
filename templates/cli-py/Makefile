.PHONY: build test lint format typecheck links check setup \
       promote promote-alpha promote-beta promote-rc \
       promote-release

check: lint typecheck test links

build:
	uv build

test:
	uv run pytest

lint:
	uv run ruff check src/ tests/

format:
	uv run ruff format src/ tests/

typecheck:
	uv run mypy src/

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
