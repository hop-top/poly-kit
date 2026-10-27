#!/usr/bin/env bash
# test-bats.sh — Run every bats suite in the repo, several at a time.
#
# Suites are discovered with `git ls-files`, so a new *.bats file is picked
# up without editing a list (untracked ones too, so a suite runs before its
# first commit). Each suite runs in its own bats process with its output
# captured; outputs print whole, in path order, once all have finished, so
# parallel runs never interleave. Exits 1 when any suite fails.
#
# bats --jobs would parallelise within bats but needs GNU parallel, which
# neither mise nor a stock macOS host provides; suites are independent
# (each works in its own mktemp dir), so file-level parallelism via
# xargs -P gets the same wall time.
#
# Usage:
#   scripts/test-bats.sh                 # every suite
#   scripts/test-bats.sh a.bats b.bats   # just these
#   BATS_JOBS=1 scripts/test-bats.sh     # serial (default: CPU count)
#
# Or: make test-bats

set -euo pipefail

cd "$(cd -- "$(dirname -- "$0")/.." && pwd)"

command -v bats >/dev/null 2>&1 || {
    echo "test-bats: bats not on PATH; run: mise install bats" >&2
    exit 2
}

if [ "$#" -gt 0 ]; then
    suites=("$@")
else
    suites=()
    while IFS= read -r f; do
        [ -f "$f" ] && suites+=("$f")
    done < <(git ls-files --cached --others --exclude-standard -- '*.bats' | sort -u)
fi
[ "${#suites[@]}" -gt 0 ] || { echo "test-bats: no suites found" >&2; exit 2; }

jobs="${BATS_JOBS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)}"

out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

# run_one <index> <suite>: run one suite, record its output, status, time.
run_one() {
    local i="$1" suite="$2" start rc=0
    start=$(date +%s)
    bats "$suite" >"$out/$i.log" 2>&1 || rc=$?
    printf '%s %s\n' "$rc" "$(($(date +%s) - start))" >"$out/$i.rc"
}
export -f run_one
export out

echo "test-bats: ${#suites[@]} suites, $jobs at a time"
start=$(date +%s)
# shellcheck disable=SC2016 # $0/$1 expand in the child bash, not here.
for i in "${!suites[@]}"; do
    printf '%s\0%s\0' "$i" "${suites[$i]}"
done | xargs -0 -n 2 -P "$jobs" bash -c 'run_one "$0" "$1"' || true
# xargs' own status is ignored: a suite with no .rc file below never
# finished, and counts as failed there.

failed=()
for i in "${!suites[@]}"; do
    suite="${suites[$i]}"
    if [ -f "$out/$i.rc" ]; then
        read -r rc secs <"$out/$i.rc"
    else
        rc=1 secs='?'
    fi
    echo
    echo "==> $suite (${secs}s, exit $rc)"
    cat "$out/$i.log" 2>/dev/null || true
    [ "$rc" -eq 0 ] || failed+=("$suite")
done

echo
echo "test-bats: ${#suites[@]} suites in $(($(date +%s) - start))s"
if [ "${#failed[@]}" -gt 0 ]; then
    echo "test-bats: ${#failed[@]} failed:" >&2
    printf '  %s\n' "${failed[@]}" >&2
    exit 1
fi
echo "test-bats: all passed"
