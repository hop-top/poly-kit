#!/usr/bin/env bash
# Fail when the hop-top-kit crate package carries anything outside its
# allowlist, or when the manifest lacks registry metadata.
#
#   scripts/check-rs-package.sh
#
# Mirrors `include` in sdk/experimental/rs/Cargo.toml: the published crate
# ships sources, examples and the licence/readme/changelog files, never
# tests, fixtures or the pinned dev toolchain. Cargo adds the generated
# files below itself.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root/sdk/experimental/rs"

list=$(mktemp)
err=$(mktemp)
trap 'rm -f "$list" "$err"' EXIT

cargo package --list --allow-dirty >"$list" 2>"$err"

# `ignoring test` warnings are expected: tests/ is excluded on purpose.
if grep -E '^warning: manifest has no' "$err"; then
  echo "error: Cargo.toml lacks registry metadata (documentation, homepage, repository)" >&2
  exit 1
fi

allow='^(src/.+|examples/.+|Cargo\.toml|Cargo\.toml\.orig|Cargo\.lock|README\.md|CHANGELOG\.md|LICENSE|NOTICE|\.cargo_vcs_info\.json)$'
extra=$(grep -Ev "$allow" "$list" || true)
if [ -n "$extra" ]; then
  echo "error: files outside the package allowlist:" >&2
  printf '  %s\n' $extra >&2
  exit 1
fi

for must in LICENSE NOTICE README.md CHANGELOG.md src/lib.rs; do
  grep -qx "$must" "$list" || { echo "error: package is missing $must" >&2; exit 1; }
done

echo "ok: $(wc -l <"$list" | tr -d ' ') files, all inside the allowlist"
