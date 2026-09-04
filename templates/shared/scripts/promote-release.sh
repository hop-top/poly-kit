#!/usr/bin/env bash
# Promote the release-please prerelease channel one step:
#   release -> alpha -> beta -> rc -> release
#
# Only `prerelease-type` changes, on every package, so the promotion gate can
# validate the transition; `prerelease: true` and `versioning: prerelease`
# stay as they are. Edits are line-based to keep the config's formatting: the
# gate rejects any changed line that is not `prerelease-type`.
#
# release-please reads `prerelease-type` only when the version it bumps has no
# prerelease suffix (the first bump after a stable version). While the
# manifest carries a suffix, every merge bumps that counter whatever the
# config says; moving x.y.z-alpha.N to x.y.z-beta.0 takes a commit carrying
# `Release-As: x.y.z-beta.0`. Stable x.y.z is cut by `Release-As: x.y.z`.
# The `release` stage cuts nothing: it resets the ladder so the next line
# can start at alpha.
set -euo pipefail

DRY_RUN=false
TMP=""
trap '[ -z "$TMP" ] || rm -f "$TMP"' EXIT

die() { echo "error: $*" >&2; exit 1; }

command -v jq >/dev/null 2>&1 || die "jq is required but not installed"

# This repo keeps the config under .github/; scaffolded repos keep it at the
# repo root. The manifest sits next to it.
CONFIG=""
for candidate in .github/release-please-config.json release-please-config.json
do
  if [ -f "$candidate" ]; then
    CONFIG="$candidate"
    break
  fi
done
[ -n "$CONFIG" ] || \
  die "release-please-config.json not found under .github/ or the repo root"
MANIFEST="$(dirname "$CONFIG")/.release-please-manifest.json"

# Stage = channel before the first dot ("alpha.0" -> "alpha"); no
# prerelease-type reads as "release". Same rule as the gate, applied to every
# package.
stage_of() {
  local stages
  stages=$(jq -r '[.packages[] | (.["prerelease-type"] // "release")
    | split(".")[0]] | unique | join(" ")' "$1")
  case "$stages" in
    "")    die "no packages in $1" ;;
    *" "*) die "packages disagree on prerelease-type: $stages" ;;
  esac
  echo "$stages"
}

# Version release-please last released for the root package (first package
# when there is no root package); empty when there is no manifest.
manifest_version() {
  [ -f "$MANIFEST" ] || return 0
  jq -r '(.["."] // (to_entries | .[0].value)) // empty' "$MANIFEST"
}

valid_next() {
  case "$1" in
    release) echo "alpha" ;;
    alpha)   echo "beta" ;;
    beta)    echo "rc" ;;
    rc)      echo "release" ;;
    *)       die "unknown stage: $1" ;;
  esac
}

# Print what each package would change to
dry_run_report() {
  local stage="$1"
  echo "[dry-run] Current stage: $CURRENT"
  echo "[dry-run] Target stage: $stage"
  echo "[dry-run] Would update $CONFIG:"
  local pkgs
  pkgs=$(jq -r '.packages | keys[]' "$CONFIG")
  while IFS= read -r pkg; do
    local cur
    cur=$(jq -r --arg p "$pkg" \
      '.packages[$p]["prerelease-type"] // "(unset)"' "$CONFIG")
    if [ "$stage" = "release" ]; then
      echo "  packages.\"$pkg\".prerelease-type: \"$cur\" → (removed)"
    else
      echo "  packages.\"$pkg\".prerelease-type: \"$cur\" → \"$stage.0\""
    fi
  done <<< "$pkgs"
  echo "[dry-run] Would commit: chore(release): promote to $stage"
  echo "[dry-run] No changes made."
}

# Rewrite prerelease-type on every package without touching any other line.
# The value carries the counter seed ("beta.0", not "beta"): without it
# release-please opens the line at x.y.z-beta and counts from beta.1. When
# no package has the key yet, it is inserted next to each `"prerelease": true`
# (below it when that line has a trailing comma, above it otherwise).
apply_stage() {
  local stage="$1" has_type got
  TMP=$(mktemp)
  has_type=$(grep -c '"prerelease-type"' "$CONFIG" || true)
  awk -v stage="$stage" -v has_type="$has_type" '
    /^[[:space:]]*"prerelease-type":/ {
      if (stage == "release") next
      sub(/"prerelease-type":[[:space:]]*"[^"]*"/,
          "\"prerelease-type\": \"" stage ".0\"")
      print
      next
    }
    /^[[:space:]]*"prerelease":[[:space:]]*true/ && stage != "release" &&
      has_type == 0 {
      match($0, /^[[:space:]]*/)
      line = substr($0, RSTART, RLENGTH) "\"prerelease-type\": \"" stage ".0\","
      if ($0 ~ /,[[:space:]]*$/) { print; print line; next }
      print line
    }
    { print }
  ' "$CONFIG" > "$TMP"

  jq -e . "$TMP" >/dev/null 2>&1 || \
    die "edit produced invalid JSON; $CONFIG left untouched"
  got=$(stage_of "$TMP")
  [ "$got" = "$stage" ] || \
    die "could not set every package to $stage (got: $got);" \
      "each package needs a \"prerelease\": true line to anchor on"
  mv "$TMP" "$CONFIG"
  TMP=""
}

# What a merged release PR proposes once this stage is in.
stage_notice() {
  local stage="$1" version base suffix="" chan=""
  version=$(manifest_version)
  echo ""
  echo "prerelease-type changed; the version itself has not moved."
  if [ -z "$version" ]; then
    echo "No manifest at $MANIFEST; see RELEASING.md for what $stage means."
    return 0
  fi
  base="${version%%-*}"
  case "$version" in
    *-*) suffix="${version#*-}"; chan="${suffix%%.*}" ;;
  esac
  echo "Manifest: $version."
  case "$stage" in
    release)
      echo "Stage 'release' cuts nothing; this script never cuts stable."
      if [ -n "$suffix" ]; then
        echo "Stable $base is cut by a commit carrying 'Release-As: $base'."
        echo "Until then a merged release PR still proposes the next $chan"
        echo "counter. Promote to alpha before that stable hits the manifest."
      else
        echo "WARNING: stable manifest and no prerelease-type: a merged release"
        echo "PR proposes a STABLE version. Promote to alpha before merging."
      fi
      ;;
    alpha)
      if [ -n "$suffix" ]; then
        echo "Merges keep bumping $chan; alpha.0 takes effect at the first bump"
        echo "after a stable version lands in the manifest."
      else
        echo "The next feat proposes the next minor as -alpha.0; a fix alone,"
        echo "the next patch as -alpha.0."
      fi
      ;;
    *)
      if [ -n "$suffix" ]; then
        echo "Merges keep bumping $chan. To move to $stage, land a commit"
        echo "carrying 'Release-As: $base-$stage.0' and dry-run release-please"
        echo "first (see RELEASING.md)."
      else
        echo "The next bump opens the new line at -$stage.0."
      fi
      ;;
  esac
}

promote() {
  local stage="$1"
  if $DRY_RUN; then
    dry_run_report "$stage"
  else
    apply_stage "$stage"
    echo "Promoted to $stage"
    git add "$CONFIG"
    git commit -m "chore(release): promote to $stage" -- "$CONFIG"
  fi
  stage_notice "$stage"
}

# Parse flags
ARGS=()
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=true ;;
    *)         ARGS+=("$arg") ;;
  esac
done
set -- "${ARGS[@]+"${ARGS[@]}"}"

if ! $DRY_RUN && ! git diff --cached --quiet; then
  die "staged changes detected. Commit or stash before promoting."
fi

CURRENT=$(stage_of "$CONFIG")
NEXT=$(valid_next "$CURRENT")

if [ $# -eq 0 ]; then
  echo "Current stage: $CURRENT"
  echo "Next stage:    $NEXT"
  if $DRY_RUN; then
    echo ""
    promote "$NEXT"
    exit 0
  fi
  printf "Promote to %s? [y/N] " "$NEXT"
  read -r ans
  case "$ans" in
    [yY]*) ;;
    *) echo "Aborted."; exit 0 ;;
  esac
  promote "$NEXT"
else
  TARGET="$1"
  [ "$TARGET" = "$NEXT" ] || \
    die "invalid transition: $CURRENT -> $TARGET (expected $NEXT)"
  promote "$TARGET"
fi
