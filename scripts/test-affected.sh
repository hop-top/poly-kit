#!/usr/bin/env bash
# test-affected.sh — run the tests a change needs.
#
# scripts/test-scope.sh decides what a change affects; this runs it.
# `make test-affected` runs every suite below, the CI test jobs one each,
# so a local run and the pull request run the same packages.
#
# Usage:
#   scripts/test-affected.sh [suite...]   run (default: go ts py rs php bats parity)
#   scripts/test-affected.sh --gate <scope suite>
#                                         CI: run=true|false into
#                                         $GITHUB_OUTPUT, before a job
#                                         installs its toolchain
#
# Suites and what each runs:
#   go              affected packages, go test -short (whole: make test-go)
#   go-integration  affected packages, go test with testcontainers and
#                   KIT_PROPERTY_ITERATIONS=$PROPERTY_ITERATIONS
#                   (whole: make test-go-integration; the CI go-test job)
#   go-race         affected packages of make test-go-race, under -race
#   ts py           make test-ts-sdk / test-ts-engine, test-py-sdk /
#                   test-py-engine per affected package (whole: test-ts,
#                   test-py)
#   rs php          make test-rs / test-php when the crate or SDK is
#                   affected (crate level)
#   bats            scripts/test-bats.sh on the affected suites
#   parity          the affected test-parity-* targets
#
# A suite runs whole when test-scope.sh says so (exit 3: TEST_ALL=1, a
# shared config or toolchain file changed, unknown base) or cannot tell
# (a tool failed). Nothing relevant changed: a skip line, exit 0.
#
# Base: test-scope.sh's (changed-files.sh): the merge-base with
# origin/next and the working tree; LINT_BASE / LINT_BASE_REF / LINT_HEAD
# as for the lint targets. BASE=<ref> is kept as LINT_BASE_REF=<ref>.
#
# Locally, rs and php are reported NOT RUN when cargo or php/composer is
# missing. With GITHUB_STEP_SUMMARY set, each suite's scope goes into the
# job summary. Exits non-zero on the first failing suite.
set -euo pipefail

cd "$(cd -- "$(dirname -- "$0")/.." && pwd)"

if [ -n "${BASE:-}" ] && [ -z "${LINT_BASE_REF:-}" ]; then
    export LINT_BASE_REF="$BASE"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

summary() {
    [ -n "${GITHUB_STEP_SUMMARY:-}" ] || return 0
    printf '\n' >>"$GITHUB_STEP_SUMMARY"
    printf '%s\n' "$@" >>"$GITHUB_STEP_SUMMARY"
}

summary_units() {
    [ -n "${GITHUB_STEP_SUMMARY:-}" ] || return 0
    {
        echo
        echo "<details><summary>$1</summary>"
        echo
        echo '```'
        cat
        echo '```'
        echo
        echo '</details>'
    } >>"$GITHUB_STEP_SUMMARY"
}

FULL_NOTE="Full suites run on pushes and on the nightly schedule."

# scope <scope suite> [--gate]: units into $tmp/units, why into $tmp/why;
# echoes affected | whole | skip.
scope() {
    local rc=0
    scripts/test-scope.sh ${2:+"$2"} "$1" >"$tmp/units" 2>"$tmp/why" || rc=$?
    case "$rc" in
        0) if [ -s "$tmp/units" ]; then echo affected; else echo skip; fi ;;
        3) echo whole ;;
        2) cat "$tmp/why" >&2; exit 2 ;;
        *) echo "test-scope.sh failed (exit $rc); running the whole suite" >>"$tmp/why"; echo whole ;;
    esac
}

if [ "${1:-}" = --gate ]; then
    [ $# -eq 2 ] || { echo "usage: test-affected.sh --gate <scope suite>" >&2; exit 2; }
    s=$2
    verdict=$(scope "$s" --gate) || exit 2
    [ -n "$verdict" ] || exit 2
    case "$verdict" in
        skip)
            run=false
            echo "test-affected: $s: nothing relevant changed; skipping."
            summary "### $s: skipped" "" "No change in this pull request reaches the $s tests." "$FULL_NOTE"
            ;;
        *)
            run=true
            echo "test-affected: $s: relevant change; running."
            ;;
    esac
    if [ -n "${GITHUB_OUTPUT:-}" ]; then
        echo "run=$run" >>"$GITHUB_OUTPUT"
    else
        echo "run=$run"
    fi
    exit 0
fi

full_target() {
    case "$1" in
        go) echo test-go ;;
        go-integration) echo test-go-integration ;;
        go-race) echo test-go-race ;;
        ts) echo test-ts ;;
        py) echo test-py ;;
        rs) echo test-rs ;;
        php) echo test-php ;;
        bats) echo test-bats ;;
        parity) echo test-parity ;;
    esac
}

pkg_target() {
    case "$1" in
        sdk/ts) echo test-ts-sdk ;;
        engine/sdk/ts-kit-engine) echo test-ts-engine ;;
        sdk/py) echo test-py-sdk ;;
        engine/sdk/py-kit-engine) echo test-py-engine ;;
        *) echo "test-affected: no make target for package $1" >&2; exit 1 ;;
    esac
}

# go_run <suite>: go test the affected packages in $tmp/units, per module.
go_run() {
    local s=$1 m pkgs flags=(-count=1 -timeout 1200s)
    case "$s" in
        go) flags+=(-short) ;;
        go-race) flags+=(-race -buildvcs=false) ;;
        go-integration) export KIT_PROPERTY_ITERATIONS="${PROPERTY_ITERATIONS:-}" ;;
    esac
    cut -f1 "$tmp/units" | sort -u >"$tmp/mods"
    while IFS= read -r m; do
        pkgs=$(awk -F'\t' -v m="$m" '$1 == m { print ($2 == "ALL" ? "./..." : $2) }' "$tmp/units" | sort -u)
        echo "==> go test ($m): $(printf '%s\n' "$pkgs" | wc -l | tr -d ' ') package(s)"
        # shellcheck disable=SC2086 # pkgs is a deliberate word list.
        (cd "$m" && go test "${flags[@]}" $pkgs) </dev/null
    done <"$tmp/mods"
}

missing_tool() {
    case "$1" in
        rs) command -v cargo >/dev/null 2>&1 || echo cargo ;;
        php) { command -v php >/dev/null 2>&1 && command -v composer >/dev/null 2>&1; } || echo php/composer ;;
    esac
}

suites=("$@")
[ "${#suites[@]}" -gt 0 ] || suites=(go ts py rs php bats parity)
not_run=()

for s in "${suites[@]}"; do
    case "$s" in
        go|go-integration|go-race|ts|py|rs|php|bats|parity) ;;
        *) echo "test-affected: unknown suite: $s" >&2; exit 2 ;;
    esac
    sc=$s
    [ "$s" != go-integration ] || sc=go
    verdict=$(scope "$sc") || exit 2
    [ -n "$verdict" ] || exit 2
    case "$verdict" in
        skip)
            echo "test-affected: $s: nothing relevant changed; skipped."
            summary "### $s: skipped" "" "No change reaches the $s tests." "$FULL_NOTE"
            continue
            ;;
        whole)
            echo "test-affected: $s: whole suite ($(tr '\n' ' ' <"$tmp/why"))"
            summary "### $s: whole suite" "" "$(sed 's/^test-scope: //' "$tmp/why")"
            ;;
        affected)
            echo "test-affected: $s: $(wc -l <"$tmp/units" | tr -d ' ') affected unit(s)"
            summary "### $s: affected only ($(wc -l <"$tmp/units" | tr -d ' '))" "" "$FULL_NOTE"
            cut -f2 "$tmp/units" | summary_units "What runs"
            ;;
    esac

    tool=$(missing_tool "$s")
    if [ -n "$tool" ]; then
        echo "test-affected: $s: NOT RUN: $tool not on PATH" >&2
        not_run+=("$s")
        continue
    fi

    if [ "$verdict" = whole ]; then
        make --no-print-directory "$(full_target "$s")"
        continue
    fi
    case "$s" in
        go|go-integration|go-race) go_run "$s" ;;
        ts|py) while IFS= read -r p; do make --no-print-directory "$(pkg_target "$p")" </dev/null; done <"$tmp/units" ;;
        rs|php) make --no-print-directory "$(full_target "$s")" ;;
        bats) xargs scripts/test-bats.sh <"$tmp/units" ;;
        parity) xargs make --no-print-directory <"$tmp/units" ;;
    esac
done

if [ "${#not_run[@]}" -gt 0 ]; then
    echo "test-affected: affected but NOT RUN (toolchain missing): ${not_run[*]}" >&2
fi
echo "test-affected: done."
