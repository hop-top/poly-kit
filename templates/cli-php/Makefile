.PHONY: build test lint links check setup \
       quality audit unused pkgcheck coverage dupes mutation \
       promote promote-alpha promote-beta promote-rc promote-release

# Line-coverage floor for `make coverage`: the scaffold ships at what
# its own starter tests reach. Raise it as tests land; never lower it
# to get a change through.
COVERAGE_MIN ?= 80

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

# quality runs the engineering gates beyond check: advisories,
# unused and undeclared dependencies, the manifest, a coverage floor
# and copy-paste. Dead code and complexity fail `make lint` through
# phpstan extensions. CI runs it on every PR and weekly.
quality: audit unused pkgcheck coverage dupes

audit:
	composer audit

unused:
	vendor/bin/composer-dependency-analyser

pkgcheck:
	composer validate --strict

# Needs a coverage driver: pcov (CI) or xdebug.
coverage:
	@XDEBUG_MODE=coverage vendor/bin/phpunit --colors=never --coverage-text --only-summary-for-coverage-text \
		| tee /dev/stderr | awk -v min=$(COVERAGE_MIN) \
		'/^ *Lines:/ { pct = $$2; sub(/%/, "", pct); found = 1 } \
		END { if (!found) { print "no coverage summary: install pcov or xdebug"; exit 1 } \
		printf "coverage %s%% (floor %s%%)\n", pct, min; exit (pct + 0 < min) }'

dupes:
	jscpd .

# mutation checks that the tests catch changed behaviour, not only
# that they run the code. Slow: CI runs it weekly, report-only.
mutation:
	vendor/bin/infection --threads=max --show-mutations

setup:
	composer install
	@command -v lychee >/dev/null 2>&1 || cargo install lychee

promote:
	@scripts/promote-release.sh

promote-alpha promote-beta promote-rc promote-release:
	@scripts/promote-release.sh $(subst promote-,,$@)
