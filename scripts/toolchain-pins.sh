#!/usr/bin/env bash
#
# toolchain-pins.sh — print the tool versions pinned in the repo's mise
# config.
#
# Two files, both loaded by mise from the repo root:
#
#   mise.toml           the kit-managed block, emitted from
#                       templates/shared/tool-versions.toml (the tools
#                       kit's scaffolds share)
#   .config/mise.toml   tools only this repo's CI uses (buf,
#                       markdownlint-cli2), kept out of the scaffolds
#
# Everything else reads the pins through this script:
#
#   - CI appends the output to $GITHUB_OUTPUT, so each setup action
#     installs the version a local `mise install` does
#     (`${{ steps.pins.outputs.node }}`).
#   - The Makefile reads the pins for the tools it installs or runs
#     itself (golangci-lint, markdownlint-cli2) and for the Go gate.
#   - scripts/check-toolchain-parity.sh compares them against the files
#     that have to carry their own copy (go.mod, rust-toolchain.toml, ...).
#
# Output is one `name=version` line per tool, in file order. A backend
# prefix is dropped from the name (`npm:markdownlint-cli2` prints as
# `markdownlint-cli2`) so every name is a valid step-output key. A tool
# declared in both files must carry the same version in both.
#
# Only plain string values are supported. A table value
# (`rust = { version = "..." }`) is refused rather than skipped, so a
# pin can never silently drop out of CI.
#
# TOOLCHAIN_PINS_FILES overrides the file list (space-separated, relative
# to the repo root); the first file must exist, the others may not.
#
# Usage:
#   scripts/toolchain-pins.sh            # every pin, name=version
#   scripts/toolchain-pins.sh <name>     # that pin's version only
# Exit:  0 ok, 1 <name> not pinned, 2 a file missing or malformed,
#        3 a tool pinned differently in two files.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

read -r -a candidates <<< "${TOOLCHAIN_PINS_FILES:-mise.toml .config/mise.toml}"
if [ ! -f "${candidates[0]}" ]; then
    echo "toolchain-pins: ${candidates[0]} not found" >&2
    exit 2
fi
files=()
for f in "${candidates[@]}"; do
    [ -f "$f" ] && files+=("$f")
done

rc=0
pins=$(awk '
    function trim(s) { sub(/^[[:space:]]+/, "", s); sub(/[[:space:]]+$/, "", s); return s }
    FNR == 1 { in_tools = 0 }
    /^[[:space:]]*\[/ {
        sec = trim($0)
        in_tools = (sec == "[tools]")
        next
    }
    !in_tools { next }
    /^[[:space:]]*(#|$)/ { next }
    {
        eq = index($0, "=")
        if (eq == 0) { printf "%s:%d: not a key = value pair\n", FILENAME, FNR > "/dev/stderr"; bad = 1; next }
        k = trim(substr($0, 1, eq - 1))
        v = trim(substr($0, eq + 1))
        sub(/[[:space:]]+#.*$/, "", v)
        if (k ~ /^".*"$/) k = substr(k, 2, length(k) - 2)
        if (v !~ /^"[^"]+"$/) { printf "%s:%d: %s: only a quoted version string is supported\n", FILENAME, FNR, k > "/dev/stderr"; bad = 1; next }
        v = substr(v, 2, length(v) - 2)
        sub(/^[a-z]+:/, "", k)
        if (k in seen) {
            if (seen[k] != v) {
                printf "%s:%d: %s %s disagrees with %s %s\n", FILENAME, FNR, k, v, where[k], seen[k] > "/dev/stderr"
                conflict = 1
            }
            next
        }
        seen[k] = v
        where[k] = FILENAME ":" FNR
        print k "=" v
        n++
    }
    END {
        if (bad) exit 2
        if (conflict) exit 3
        if (!n) { print "no [tools] entries" > "/dev/stderr"; exit 2 }
    }
' "${files[@]}") || rc=$?
case "$rc" in
    0) ;;
    3) echo "toolchain-pins: a tool is pinned differently in two mise config files" >&2; exit 3 ;;
    *) echo "toolchain-pins: cannot read pins from ${files[*]}" >&2; exit 2 ;;
esac

if [ $# -eq 0 ]; then
    printf '%s\n' "$pins"
    exit 0
fi

version=$(printf '%s\n' "$pins" | awk -F= -v want="$1" '$1 == want { print $2; exit }')
if [ -z "$version" ]; then
    echo "toolchain-pins: $1 is not pinned in ${files[*]}" >&2
    exit 1
fi
printf '%s\n' "$version"
