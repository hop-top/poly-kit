.PHONY: build test lint links check setup \
       promote promote-alpha promote-beta promote-rc promote-release

check: lint test links

build:
	composer install --no-dev --optimize-autoloader

test:
	composer test

lint:
	composer lint

links:
	@if command -v lychee >/dev/null 2>&1; then \
		lychee --no-progress .; \
	else \
		echo "lychee not installed; skipping link check"; \
	fi

setup:
	composer install
	@command -v lychee >/dev/null 2>&1 || cargo install lychee

promote:
	@scripts/promote-release.sh

promote-alpha promote-beta promote-rc promote-release:
	@scripts/promote-release.sh $(subst promote-,,$@)
