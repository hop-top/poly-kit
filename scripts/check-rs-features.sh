#!/usr/bin/env bash
#
# check-rs-features.sh — compile the Rust SDK once per Cargo feature,
# each on its own, tests and examples included.
#
# `default = []` and every module is feature-gated, so `cargo test` and
# `cargo test --all-features` never build a feature without the ones it
# happens to be unified with. A source or test file that names another
# feature's items without declaring the dependency compiles in both of
# those runs and breaks for the consumer who enables only its feature.
#
# The feature list is read from the [features] table of Cargo.toml, so a
# new feature is covered without editing this script. The empty set
# (default features only) is checked first.
#
# Usage: scripts/check-rs-features.sh [crate-dir]   (default sdk/rs)
# Env:   CARGO_FLAGS  extra flags for every cargo invocation (default --locked)
# Exit:  0 every feature compiles, 1 one or more failed, 2 usage error.
set -euo pipefail

cd "$(cd -- "$(dirname -- "$0")/.." && pwd)"
dir="${1:-sdk/rs}"
manifest="$dir/Cargo.toml"
[ -f "$manifest" ] || { echo "check-rs-features: no $manifest" >&2; exit 2; }

features=$(awk '
    /^\[features\]/ { in_f = 1; next }
    /^\[/           { in_f = 0 }
    in_f && /^[A-Za-z0-9_-]+[ \t]*=/ { sub(/[ \t]*=.*/, ""); if ($0 != "default") print }
' "$manifest")
[ -n "$features" ] || { echo "check-rs-features: no features in $manifest" >&2; exit 2; }

# shellcheck disable=SC2206 # CARGO_FLAGS is a deliberate word list.
flags=(${CARGO_FLAGS:---locked})
failed=()

check() {
    label=$1
    shift
    echo "==> $label"
    if ! (cd "$dir" && cargo check "${flags[@]}" --all-targets --no-default-features "$@"); then
        failed+=("$label")
    fi
}

check "(no features)"
for f in $features; do
    check "$f" --features "$f"
done

if [ "${#failed[@]}" -gt 0 ]; then
    echo >&2
    echo "check-rs-features: does not compile alone: ${failed[*]}" >&2
    exit 1
fi
echo "check-rs-features: every feature compiles alone"
