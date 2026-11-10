.PHONY: build test lint links check setup \
       quality audit unused pkgcheck coverage dupes mutation \
       promote promote-alpha promote-beta promote-rc \
       promote-release

# Line-coverage floor for `make coverage`: the scaffold ships at what
# its own starter tests reach. Raise it as tests land; never lower it
# to get a change through.
COVERAGE_MIN ?= 80

check: lint test links

build:
	pnpm build

test:
	pnpm vitest run

lint:
	pnpm lint

# quality runs the engineering gates beyond check: known
# vulnerabilities, unused files, exports and dependencies, the
# publishable package, a coverage floor and copy-paste. CI runs it on
# every PR and weekly.
quality: audit unused pkgcheck coverage dupes

# Shipped dependencies block from moderate up; dev tooling, which
# never reaches a user, from high up.
audit:
	pnpm audit --prod --audit-level moderate
	pnpm audit --audit-level high

unused:
	pnpm run unused

pkgcheck: build
	pnpm run pkgcheck

coverage:
	pnpm run coverage --coverage.thresholds.lines=$(COVERAGE_MIN)

dupes:
	jscpd .

# mutation checks that the tests catch changed behaviour, not only
# that they run the code. Slow: CI runs it weekly, report-only.
mutation:
	pnpm run mutation

links:
	@if command -v lychee >/dev/null 2>&1; then \
		lychee --no-progress .; \
	else \
		echo "lychee not installed; skipping link check"; \
	fi

setup:
	pnpm install
	@command -v lychee >/dev/null 2>&1 || cargo install lychee

promote:
	@scripts/promote-release.sh

promote-alpha promote-beta promote-rc promote-release:
	@scripts/promote-release.sh $(subst promote-,,$@)
