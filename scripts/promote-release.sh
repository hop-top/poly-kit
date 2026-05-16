#!/usr/bin/env bash
set -euo pipefail

CONFIG=".github/release-please-config.json"
DRY_RUN=false

die() { echo "error: $*" >&2; exit 1; }

command -v jq >/dev/null 2>&1 || die "jq is required but not installed"
[ -f "$CONFIG" ] || die "$CONFIG not found"

current_stage() {
  jq -r '.packages["."]["prerelease-type"] // "release"' "$CONFIG"
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
      '.packages[$p]["prerelease-type"] // "release"' "$CONFIG")
    if [ "$stage" = "release" ]; then
      echo "  packages.\"$pkg\".prerelease-type: \"$cur\" → (removed)"
    else
      echo "  packages.\"$pkg\".prerelease-type: \"$cur\" → \"$stage\""
    fi
  done <<< "$pkgs"
  echo "[dry-run] Would commit: chore(release): promote to $stage"
  echo "[dry-run] No changes made."
}

apply_stage() {
  local stage="$1"
  local tmp
  tmp=$(mktemp)

  if [ "$stage" = "release" ]; then
    jq '.packages |= with_entries(
      .value |= del(.["prerelease-type"])
    )' "$CONFIG" > "$tmp"
  else
    jq --arg s "$stage" '.packages |= with_entries(
      .value["prerelease-type"] = $s
    )' "$CONFIG" > "$tmp"
  fi

  mv "$tmp" "$CONFIG"
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

CURRENT=$(current_stage)

if [ $# -eq 0 ]; then
  NEXT=$(valid_next "$CURRENT")
  echo "Current stage: $CURRENT"
  echo "Next stage:    $NEXT"
  if $DRY_RUN; then
    echo ""
    dry_run_report "$NEXT"
    exit 0
  fi
  printf "Promote to %s? [y/N] " "$NEXT"
  read -r ans
  case "$ans" in
    [yY]*) ;;
    *) echo "Aborted."; exit 0 ;;
  esac
  apply_stage "$NEXT"
  echo "Promoted to $NEXT"
  git add "$CONFIG"
  git commit -m "chore(release): promote to $NEXT" -- "$CONFIG"
else
  TARGET="$1"
  NEXT=$(valid_next "$CURRENT")
  [ "$TARGET" = "$NEXT" ] || \
    die "invalid transition: $CURRENT -> $TARGET (expected $NEXT)"
  if $DRY_RUN; then
    dry_run_report "$TARGET"
    exit 0
  fi
  apply_stage "$TARGET"
  echo "Promoted to $TARGET"
  git add "$CONFIG"
  git commit -m "chore(release): promote to $TARGET" -- "$CONFIG"
fi
