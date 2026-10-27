#!/usr/bin/env bash
#
# lint-go.sh — golangci-lint over the Go packages a change touches.
#
# Linted modules: the root module plus every go.mod under go/ cmd/
# contracts/ engine/ examples/ incubator/ (outside testdata/).
#
# Changed mode (default): the packages holding a changed or deleted .go
# file (scripts/changed-files.sh decides what changed), passed to
# golangci-lint by directory, so it type-checks those packages and the
# export data of their imports instead of the whole tree. Whole packages
# are linted, not just the changed lines: a package is type-checked in
# full either way, and package-wide checks (unused) would otherwise miss
# an issue a change creates in an untouched sibling file.
#
# A module whose go.mod or go.sum changed is linted in full (a dependency
# bump can break code the change never touched). Everything is linted
# when LINT_ALL is set, or .golangci.yml, mise.toml (Go and golangci-lint
# pins), this script or the changed-files helper changed.
#
# Env:
#   GOLANGCI_LINT        golangci-lint binary (default: golangci-lint)
#   GOLANGCI_LINT_FLAGS  extra `run` flags, e.g. "--concurrency=4"
#   LINT_ALL, LINT_BASE, LINT_HEAD, LINT_BASE_REF: see changed-files.sh
#
# Exit: golangci-lint's status; 0 when no Go file changed.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

GOLANGCI_LINT=${GOLANGCI_LINT:-golangci-lint}
export GOFLAGS=-buildvcs=false
# shellcheck disable=SC2206 # Deliberate word split of a flag list.
extra=(${GOLANGCI_LINT_FLAGS:-})

modules() {
    printf '.\n'
    find go cmd contracts engine examples incubator -name go.mod -not -path '*/testdata/*' \
        -exec dirname {} \; 2>/dev/null | sort
}

run_lint() { # <module dir> <package>...
    local mod=$1; shift
    echo "==> golangci-lint ($mod): $*"
    (cd "$mod" && "$GOLANGCI_LINT" run ${extra[@]+"${extra[@]}"} "$@")
}

full_lint() {
    local mod rc=0
    while IFS= read -r mod; do
        run_lint "$mod" ./... || rc=1
    done <<<"$linted"
    exit "$rc"
}

linted=$(modules)

rc=0
changed=$(scripts/changed-files.sh --include-deleted \
    --trigger .golangci.yml --trigger mise.toml --trigger scripts/lint-go.sh \
    -- '*.go' '*go.mod' '*go.sum') || rc=$?
case "$rc" in
    0) ;;
    3) full_lint ;;
    *) exit "$rc" ;;
esac

if [ -z "$changed" ]; then
    echo "==> lint-go: no Go changes, skipped (LINT_ALL=1 lints everything)"
    exit 0
fi

# Nearest linted module holding <dir>; empty when none (e.g. a module
# outside the linted roots, or a testdata tree).
module_of() {
    local d=$1 mod
    while :; do
        if [ -f "$d/go.mod" ]; then
            mod=$d
            break
        fi
        [ "$d" != . ] || return 0
        d=$(dirname "$d")
    done
    if printf '%s\n' "$linted" | grep -xF -- "$mod" >/dev/null; then
        printf '%s\n' "$mod"
    fi
}

# One "<module>\t<package dir|*>" line per changed path; "*" lints the
# whole module.
collect_targets() {
    local f d mod rel
    while IFS= read -r f; do
        d=$(dirname "$f")
        case "/$d/" in
            */testdata/*|*/vendor/*|*/node_modules/*|*/_*|*/.[!/]*) continue ;;
        esac
        # A deleted package leaves no directory to lint.
        while [ ! -d "$d" ]; do d=$(dirname "$d"); done
        mod=$(module_of "$d")
        [ -n "$mod" ] || continue
        case "$f" in
            go.mod|go.sum|*/go.mod|*/go.sum) printf '%s\t*\n' "$mod"; continue ;;
        esac
        [ "$d" = "$(dirname "$f")" ] || continue
        rel=${d#"$mod"}; rel=${rel#/}
        printf '%s\t./%s\n' "$mod" "$rel"
    done <<<"$changed"
}
targets=$(collect_targets | sort -u)

status=0
for mod in $(printf '%s\n' "$targets" | cut -f1 | sort -u); do
    dirs=$(printf '%s\n' "$targets" | awk -F'\t' -v m="$mod" '$1 == m { print $2 }')
    if printf '%s\n' "$dirs" | grep -qxF '*'; then
        run_lint "$mod" ./... || status=1
        continue
    fi
    # Keep directories that still hold Go files the build sees; one whose
    # files are all excluded by build tags is not a loadable package.
    # shellcheck disable=SC2086 # $dirs: one directory per word.
    pkgs=$(cd "$mod" && go list -e -find \
        -f '{{if or .GoFiles .CgoFiles .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' \
        $dirs 2>/dev/null | sed '/^$/d')
    if [ -z "$pkgs" ]; then
        echo "==> lint-go ($mod): changed directories hold no buildable Go package, skipped"
        continue
    fi
    # shellcheck disable=SC2086 # $pkgs: one import path per word.
    run_lint "$mod" $pkgs || status=1
done
exit "$status"
