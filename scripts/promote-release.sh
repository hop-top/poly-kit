#!/usr/bin/env bash
# promote-release.sh — record a target-versioning step in
# .github/release-please-config.json: promote a package's line to the next
# channel (x-channel), open a line (x-open-line), or clear a record.
#
# A wrapper around bin/promote.js of hop-top/.github's release-please
# extension (scripts/release-please-target), run at the commit
# .github/workflows/release-please.yml pins: the code the release run itself
# uses, so a record this writes is one that run accepts. Arguments pass
# through to promote.js, from the repository root:
#
#   scripts/promote-release.sh channel beta --package kit --package qmochi
#   scripts/promote-release.sh channel rc --group kit
#   scripts/promote-release.sh open-line 1.0.0 --group kit
#   scripts/promote-release.sh clear channel --package kit
#   scripts/promote-release.sh channel beta --package kit --dry-run
#   scripts/promote-release.sh --pin      # print the pinned hop-top/.github commit
#
# The config change is committed on the current branch, alone, as
# `chore(release): promote kit and qmochi to beta` (`open 1.0.0 for group
# kit`, `clear channel for kit`), with promote.js's report as the body; push
# it to next, where the release-please run cuts the promoted packages'
# release PRs. Staged changes, or uncommitted changes in the config, refuse
# the run. Nothing changed, nothing committed. --dry-run prints and writes
# nothing; --no-commit (kept from promote.js) writes without committing, as
# the promotion gate replays records. RELEASING.md has the rules.
#
# The extension comes from RELEASE_PLEASE_TARGET_DIR (a
# scripts/release-please-target directory with its dependencies installed)
# when set; otherwise from a sparse checkout of hop-top/.github at the
# pinned commit (a full SHA; anything else is refused), cached under
# ${XDG_CACHE_HOME:-~/.cache}/poly-kit/release-please-target/<sha> and
# installed with `npm ci --ignore-scripts` on first use (git, npm and
# network needed once; the caches of other pins unused for 14 days are
# removed then). NODE names the node binary (default: node, >= 20).
#
# Exit: promote.js's own (0 written or nothing to do, 1 refused, 2 usage);
# 1 also for staged or uncommitted changes; 3 when the extension cannot be
# set up.
#
# Not to be confused with templates/shared/scripts/promote-release.sh, the
# one scaffolded repositories get: it drives release-please's stock
# `versioning: "prerelease"` that `kit init` seeds, and stays as it is until
# the templates move to target versioning.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workflow="$root/.github/workflows/release-please.yml"
NODE="${NODE:-node}"
CACHE_MAX_IDLE_DAYS=14

die() { echo "promote-release: $*" >&2; exit 3; }
refuse() { echo "promote-release: $*" >&2; exit 1; }

# The ref release-please.yml calls release-please-on-push.yml at.
pinned_ref() {
  local refs
  [ -f "$workflow" ] || die "$workflow not found"
  refs=$(sed -n 's|^[[:space:]]*uses:[[:space:]]*hop-top/\.github/\.github/workflows/release-please-on-push\.yml@\([^[:space:]#]*\).*$|\1|p' "$workflow")
  case "$refs" in
    "")    die "no hop-top/.github release-please-on-push.yml call in $workflow" ;;
    *$'\n'*) die "more than one release-please-on-push.yml call in $workflow" ;;
  esac
  # A tag or branch could move under the release run and the promotion gate.
  [[ "$refs" =~ ^[0-9a-f]{40}$ ]] ||
    die "pinned ref '$refs' is not a full commit SHA: $workflow must call release-please-on-push.yml at a 40-character commit SHA"
  echo "$refs"
}

# Sparse checkout of scripts/release-please-target at <ref> into <dir>,
# dependencies installed; built aside and moved in only once complete. A
# run that finished first keeps its copy. Once this one is in place, the
# caches of other pins unused for CACHE_MAX_IDLE_DAYS are removed: each use
# touches its cache, so worktrees on different pins keep theirs.
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
  if [ -e "$dir" ]; then
    rm -rf "$tmp"
  else
    mv "$tmp" "$dir"
  fi
  local other
  while IFS= read -r other; do
    if [ "$other" != "$dir" ] && [[ "${other##*/}" =~ ^[0-9a-f]{40}$ ]]; then
      rm -rf "$other"
    fi
  done < <(find "$(dirname "$dir")" -mindepth 1 -maxdepth 1 -type d -mtime +"$CACHE_MAX_IDLE_DAYS")
}

extension_dir() {
  local ref cache
  if [ -n "${RELEASE_PLEASE_TARGET_DIR:-}" ]; then
    echo "$RELEASE_PLEASE_TARGET_DIR"
    return
  fi
  ref=$(pinned_ref)
  cache="${XDG_CACHE_HOME:-$HOME/.cache}/poly-kit/release-please-target/$ref"
  [ -f "$cache/scripts/release-please-target/bin/promote.js" ] || fetch_extension "$ref" "$cache"
  touch "$cache" 2>/dev/null || : # last use, for pruning
  echo "$cache/scripts/release-please-target"
}

# "a", "a and b", "a, b and c"
join_names() {
  local n=$# i=0 x out=""
  for x in "$@"; do
    i=$((i + 1))
    if [ "$i" -eq 1 ]; then
      out=$x
    elif [ "$i" -eq "$n" ]; then
      out="$out and $x"
    else
      out="$out, $x"
    fi
  done
  echo "$out"
}

if [ "${1:-}" = "--pin" ]; then
  pinned_ref
  exit 0
fi

# promote.js's arguments, minus --no-commit; what the commit subject names.
args=() positional=() names=()
config=.github/release-please-config.json
dry_run=false commit=true expect=""
for a in "$@"; do
  if [ -n "$expect" ]; then
    case "$expect" in
      package) names+=("$a") ;;
      group) names+=("group $a") ;;
      config) config=$a ;;
    esac
    expect="" args+=("$a")
    continue
  fi
  case "$a" in
    --no-commit) commit=false; continue ;;
    --dry-run) dry_run=true ;;
    --package | -p) expect=package ;;
    --group | -g) expect=group ;;
    --config) expect=config ;;
    --manifest) expect=manifest ;;
    --package=*) names+=("${a#--package=}") ;;
    --group=*) names+=("group ${a#--group=}") ;;
    --config=*) config=${a#--config=} ;;
    -*) ;;
    *) positional+=("$a") ;;
  esac
  args+=("$a")
done

cd "$root"
if $dry_run || ! $commit; then
  commit=false
else
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 ||
    refuse "$root is not a git work tree (--no-commit writes without committing)"
  git diff --cached --quiet ||
    refuse "staged changes detected: commit or unstage them before promoting"
  git diff --quiet -- "$config" ||
    refuse "$config has uncommitted changes: commit or discard them before promoting"
fi

ext=$(extension_dir)
[ -f "$ext/bin/promote.js" ] || die "$ext/bin/promote.js not found"
command -v "$NODE" >/dev/null 2>&1 || die "node is required (>= 20)"

if ! $commit; then
  exec "$NODE" "$ext/bin/promote.js" "${args[@]+"${args[@]}"}"
fi

rc=0
report=$("$NODE" "$ext/bin/promote.js" "${args[@]+"${args[@]}"}") || rc=$?
[ -z "$report" ] || printf '%s\n' "$report"
[ "$rc" -eq 0 ] || exit "$rc"
if git diff --quiet -- "$config"; then
  exit 0
fi

who=$(join_names "${names[@]+"${names[@]}"}")
command=${positional[0]:-} value=${positional[1]:-}
case "$command" in
  channel) subject="promote $who to $value" ;;
  open-line) subject="open $value for $who" ;;
  clear)
    if [ "$value" = all ]; then
      subject="clear records of $who"
    else
      subject="clear $value for $who"
    fi
    ;;
  *) subject="update promotion records of $who" ;;
esac
git commit -q -m "chore(release): $subject" -m "$report" -- "$config"
echo "committed: chore(release): $subject (push it to next)"
