#!/usr/bin/env bash
#
# toolchain-pins.sh — print the tool versions pinned in mise.toml.
#
# mise.toml's [tools] table is the one place this repo pins its
# toolchain. Everything else reads it through this script:
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
# `markdownlint-cli2`) so every name is a valid step-output key.
#
# Only plain string values are supported. A table value
# (`rust = { version = "..." }`) is refused rather than skipped, so a
# pin can never silently drop out of CI.
#
# Usage:
#   scripts/toolchain-pins.sh            # every pin, name=version
#   scripts/toolchain-pins.sh <name>     # that pin's version only
# Exit:  0 ok, 1 <name> not pinned, 2 mise.toml missing or malformed.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
file="${TOOLCHAIN_PINS_FILE:-$root/mise.toml}"

if [ ! -f "$file" ]; then
    echo "toolchain-pins: $file not found" >&2
    exit 2
fi

pins=$(awk '
    function trim(s) { sub(/^[[:space:]]+/, "", s); sub(/[[:space:]]+$/, "", s); return s }
    /^[[:space:]]*\[/ {
        sec = trim($0)
        in_tools = (sec == "[tools]")
        next
    }
    !in_tools { next }
    /^[[:space:]]*(#|$)/ { next }
    {
        eq = index($0, "=")
        if (eq == 0) { printf "line %d: not a key = value pair\n", NR > "/dev/stderr"; bad = 1; next }
        k = trim(substr($0, 1, eq - 1))
        v = trim(substr($0, eq + 1))
        sub(/[[:space:]]+#.*$/, "", v)
        if (k ~ /^".*"$/) k = substr(k, 2, length(k) - 2)
        if (v !~ /^"[^"]+"$/) { printf "line %d: %s: only a quoted version string is supported\n", NR, k > "/dev/stderr"; bad = 1; next }
        v = substr(v, 2, length(v) - 2)
        sub(/^[a-z]+:/, "", k)
        print k "=" v
        n++
    }
    END {
        if (bad) exit 2
        if (!n) { print "no [tools] entries" > "/dev/stderr"; exit 2 }
    }
' "$file") || {
    echo "toolchain-pins: cannot read pins from $file" >&2
    exit 2
}

if [ $# -eq 0 ]; then
    printf '%s\n' "$pins"
    exit 0
fi

version=$(printf '%s\n' "$pins" | awk -F= -v want="$1" '$1 == want { print $2; exit }')
if [ -z "$version" ]; then
    echo "toolchain-pins: $1 is not pinned in $file" >&2
    exit 1
fi
printf '%s\n' "$version"
