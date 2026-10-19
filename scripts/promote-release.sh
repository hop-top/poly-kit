#!/usr/bin/env bash
# promote-release.sh — record a target-versioning step in
# .github/release-please-config.json: promote a package's line to the next
# channel (x-channel), open a line (x-open-line), or clear a record.
#
# A thin wrapper around bin/promote.js of hop-top/.github's release-please
# extension (scripts/release-please-target), run at the commit
# .github/workflows/release-please.yml pins: the code the release run itself
# uses, so a record this writes is one that run accepts. Arguments pass
# through unchanged, from the repository root:
#
#   scripts/promote-release.sh channel beta --package kit --package qmochi
#   scripts/promote-release.sh channel rc --group kit
#   scripts/promote-release.sh open-line 1.0.0 --group kit
#   scripts/promote-release.sh clear channel --package kit
#   scripts/promote-release.sh channel beta --package kit --dry-run
#   scripts/promote-release.sh --pin      # print the pinned hop-top/.github ref
#
# It writes the config only; commit it to next, where the release-please run
# on that push cuts the promoted packages' release PRs. RELEASING.md has the
# rules.
#
# The extension comes from RELEASE_PLEASE_TARGET_DIR (a
# scripts/release-please-target directory with its dependencies installed,
# as the promotion gate sets up) when set; otherwise from a sparse checkout
# of hop-top/.github at the pinned ref, cached under
# ${XDG_CACHE_HOME:-~/.cache}/poly-kit/release-please-target/<ref> and
# installed with `npm ci --ignore-scripts` on first use (git, npm and
# network needed once). NODE names the node binary (default: node, >= 20).
#
# Exit: promote.js's own (0 written or nothing to do, 1 refused, 2 usage);
# 3 when the extension cannot be set up.
#
# Not to be confused with templates/shared/scripts/promote-release.sh, the
# one scaffolded repositories get: it drives release-please's stock
# `versioning: "prerelease"` that `kit init` seeds, and stays as it is until
# the templates move to target versioning.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workflow="$root/.github/workflows/release-please.yml"
NODE="${NODE:-node}"

die() { echo "promote-release: $*" >&2; exit 3; }

# The ref release-please.yml calls release-please-on-push.yml at.
pinned_ref() {
  local refs
  [ -f "$workflow" ] || die "$workflow not found"
  refs=$(sed -n 's|^[[:space:]]*uses:[[:space:]]*hop-top/\.github/\.github/workflows/release-please-on-push\.yml@\([^[:space:]#]*\).*$|\1|p' "$workflow")
  case "$refs" in
    "")    die "no hop-top/.github release-please-on-push.yml call in $workflow" ;;
    *$'\n'*) die "more than one release-please-on-push.yml call in $workflow" ;;
  esac
  [[ "$refs" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ ]] || die "pinned ref '$refs' is not a git ref or SHA"
  echo "$refs"
}

# Sparse checkout of scripts/release-please-target at <ref> into <dir>,
# dependencies installed; built aside and moved in only once complete.
fetch_extension() {
  local ref="$1" dir="$2" tmp
  command -v git >/dev/null 2>&1 || die "git is required to fetch the extension"
  command -v npm >/dev/null 2>&1 || die "npm is required to install the extension"
  mkdir -p "$(dirname "$dir")"
  tmp=$(mktemp -d "$dir.XXXXXX")
  echo "promote-release: fetching hop-top/.github@$ref (scripts/release-please-target)" >&2
  if ! {
    git -C "$tmp" init -q &&
      git -C "$tmp" remote add origin https://github.com/hop-top/.github.git &&
      git -C "$tmp" sparse-checkout set --no-cone /scripts/release-please-target/ &&
      git -C "$tmp" fetch -q --depth 1 origin "$ref" &&
      git -C "$tmp" checkout -q FETCH_HEAD &&
      (cd "$tmp/scripts/release-please-target" &&
        npm ci --ignore-scripts --no-audit --no-fund --loglevel=error >&2)
  }; then
    rm -rf "$tmp"
    die "could not fetch or install hop-top/.github@$ref"
  fi
  mv "$tmp" "$dir"
}

extension_dir() {
  local ref cache
  if [ -n "${RELEASE_PLEASE_TARGET_DIR:-}" ]; then
    echo "$RELEASE_PLEASE_TARGET_DIR"
    return
  fi
  ref=$(pinned_ref)
  cache="${XDG_CACHE_HOME:-$HOME/.cache}/poly-kit/release-please-target/${ref//\//_}"
  [ -f "$cache/scripts/release-please-target/bin/promote.js" ] || fetch_extension "$ref" "$cache"
  echo "$cache/scripts/release-please-target"
}

if [ "${1:-}" = "--pin" ]; then
  pinned_ref
  exit 0
fi

ext=$(extension_dir)
[ -f "$ext/bin/promote.js" ] || die "$ext/bin/promote.js not found"
command -v "$NODE" >/dev/null 2>&1 || die "node is required (>= 20)"

cd "$root"
exec "$NODE" "$ext/bin/promote.js" "$@"
