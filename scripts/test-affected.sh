#!/bin/sh
# test-affected.sh — Run the tests implicated by a range of changes.
#
# This carries the per-language test selection that used to live inline in
# `.githooks/pre-push`. It was moved OUT of the push path: the full suite
# (test-go + test-ts + test-py + test-parity) runs for many minutes, and
# GitHub's SSH server closes the connection long before it finishes, so the
# push died mid-gate. A gate that cannot finish inside a push is a gate
# people learn to bypass with --no-verify, which defeats it entirely.
#
# Run it yourself, or let a pre-commit hook run it:
#
#   make test-affected                  # vs the merge-base with origin/next
#   BASE=origin/main make test-affected # vs another base
#   scripts/test-affected.sh <base-ref>
#
# Exits non-zero on the first failing suite.

set -e

cd "$(cd -- "$(dirname -- "$0")/.." && pwd)"

BASE_REF="${1:-${BASE:-}}"
if [ -z "$BASE_REF" ]; then
    for cand in origin/next origin/main next main; do
        if git rev-parse --verify --quiet "$cand^{commit}" >/dev/null; then
            BASE_REF="$cand"
            break
        fi
    done
fi
[ -n "$BASE_REF" ] || BASE_REF="HEAD~1"

BASE_SHA=$(git merge-base HEAD "$BASE_REF" 2>/dev/null || git rev-parse "$BASE_REF")
CHANGED=$(git diff --name-only "$BASE_SHA" HEAD)
# Include uncommitted work so a pre-commit run sees what is being committed.
CHANGED="$CHANGED
$(git diff --name-only HEAD 2>/dev/null || true)
$(git diff --cached --name-only 2>/dev/null || true)"
CHANGED=$(printf '%s\n' "$CHANGED" | sort -u | sed '/^$/d')

if [ -z "$CHANGED" ]; then
    echo "test-affected: no changes vs $BASE_REF; nothing to test."
    exit 0
fi

HAS_GO=$(echo "$CHANGED" | grep -E '\.go$' || true)
HAS_TS=$(echo "$CHANGED" | grep -E '^sdk/ts/' || true)
HAS_PY=$(echo "$CHANGED" | grep -E '^sdk/py/' || true)
HAS_CONTRACTS=$(echo "$CHANGED" | grep -E '^contracts/' || true)

echo "test-affected: base $BASE_REF ($BASE_SHA)"


if [ -n "$HAS_GO" ] || [ -n "$HAS_CONTRACTS" ]; then
    if [ -n "$HAS_GO" ]; then
        # Extract unique Go packages from changed .go files, skipping
        # directories that no longer exist (e.g. deleted in this push) and
        # directories holding no .go files of their own (a parent that only
        # carries subpackages) — `go test` on either yields "[setup failed]".
        # Packages inside a NESTED module (their own go.mod, e.g.
        # extensions/mcp-tasks) cannot be resolved by the root module's
        # `go test` — passing them yields "[setup failed]" too. Split them
        # out and test each nested module from its own directory.
        ALL_DIRS=$(echo "$CHANGED" | grep -E '\.go$' \
            | xargs -I{} dirname {} \
            | sort -u \
            | while IFS= read -r d; do
                [ -d "$d" ] || continue
                for f in "$d"/*.go; do
                    [ -e "$f" ] || continue
                    printf '%s\n' "$d"
                    break
                done
              done)

        GO_PKGS=""
        NESTED_MODS=""
        for d in $ALL_DIRS; do
            # Walk up from $d looking for a go.mod above the repo root.
            m="$d"
            root_mod=""
            while [ "$m" != "." ] && [ -n "$m" ]; do
                if [ -f "$m/go.mod" ]; then root_mod="$m"; break; fi
                m=$(dirname "$m")
            done
            if [ -n "$root_mod" ]; then
                case " $NESTED_MODS " in
                    *" $root_mod "*) ;;
                    *) NESTED_MODS="$NESTED_MODS $root_mod" ;;
                esac
            else
                GO_PKGS="$GO_PKGS ./$d"
            fi
        done

        if [ -n "$GO_PKGS" ]; then
            echo "  go (affected packages)..."
            # shellcheck disable=SC2086 # GO_PKGS is a deliberate word list.
            go test -short -count=1 $GO_PKGS
        fi
        for m in $NESTED_MODS; do
            echo "  go (nested module $m)..."
            ( cd "$m" && go test -short -count=1 ./... )
        done
    else
        echo "  go (all, due to contract changes)..."
        make test-go
    fi
fi

[ -n "$HAS_TS" ] && { echo "  ts..."; make test-ts; }
[ -n "$HAS_PY" ] && { echo "  py..."; make test-py; }

# Parity tests when CLI or contracts change.
HAS_CLI=$(echo "$CHANGED" | grep -E '^go/console/cli/' || true)
if [ -n "$HAS_CLI" ] || [ -n "$HAS_CONTRACTS" ]; then
    echo "Running parity tests..."
    make test-parity
fi

echo "test-affected: all affected suites passed."
