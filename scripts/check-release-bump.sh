#!/usr/bin/env bash
#
# check-release-bump.sh — fail a release-please PR that breaks the repo's
# release rules (RELEASING.md, "Breaking changes start a new minor line" and
# "Linked releases"):
#
#   stale      the proposed version is already tagged, or the package's
#              release-as pin is not newer than its current version; a pin
#              left behind re-proposes a version that exists
#   breaking   a breaking commit landed since the package's last release, but
#              the proposed version is not x.(y+1).0-alpha.0 while the major
#              is 0, (x+1).0.0-alpha.0 from 1.0, or the stable form of either
#   linked     the package is in a linked group and the proposed version
#              starts a new major, or a new minor with a prerelease suffix,
#              but not every group member releases at that version in this PR
#   pins       <head>'s config still holds a release-as pin <head>'s manifest
#              has reached, or a linked-versions plugin entry whose members
#              keep no pin: the pins job has not dropped them yet, and merged
#              now they would stay on the base branch
#
# It checks every package whose manifest entry differs between <head> and
# the merge base of <base> and <head>, plus the packages the head branch
# names (release-please--branches--<b>--components--<component>, or
# --groups--<group> for a linked group): a PR that re-proposes a released
# version leaves the manifest as it was. release-please builds the PR from
# the merge base, so its config and manifest say what the PR started from,
# even after a sibling release moved <base> on; only the pins rule reads
# <head>'s config, which the pins job edits on the PR branch. For each
# package:
#
#   current   its version in the merge base's manifest
#   proposed  its version in <head>'s manifest
#   breaking  non-merge commits in <tag>..<head> that release-please routes to
#             the package (exclude-paths included) and parses as breaking;
#             <tag> is the package's release tag of <current>, named from the
#             config (e.g. kit/v0.5.0-alpha.26)
#
# scripts/release-lib.sh holds the routing and parsing rules. Tags must be
# present locally (CI: actions/checkout with fetch-depth: 0). Each failure
# prints the scripts/release-as.sh command that fixes it.
#
# Usage:
#   scripts/check-release-bump.sh [--branch <name>] [--head-ref <name>]
#     [--config <path>] [--manifest <path>] <base> <head>
#
#   scripts/check-release-bump.sh origin/next \
#     origin/release-please--branches--next--groups--kit
#
# RELEASE_AS_CMD names scripts/release-as.sh in the fix instructions when it
# lives elsewhere. --branch names the release PR's base branch there;
# defaults to <base> without a leading "origin/". --head-ref is the PR's head
# branch name; defaults to <head> without a leading "origin/". --config defaults to
# .github/release-please-config.json, else release-please-config.json, and
# --manifest to .release-please-manifest.json next to the config.
#
# Exit: 0 nothing to fix, 1 a rule is broken,
#       2 usage error, missing config, manifest or tag.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/release-lib.sh
. "$SCRIPT_DIR/release-lib.sh"

die() { echo "error: $*" >&2; exit 2; }

usage() {
  sed -n '/^# Usage:/,/^# Exit:/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
  exit 2
}

# fail <title> <one-line summary> — mark the run failed and, in GitHub
# Actions, annotate it; the details go to stderr before the call.
fail() {
  FAILED=1
  [ "${GITHUB_ACTIONS:-}" = "true" ] || return 0
  echo "::error title=$1::$2"
}

BRANCH="" HEAD_REF="" CONFIG_OPT="" MANIFEST_OPT=""
ARGS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --branch|--head-ref|--config|--manifest)
      [ $# -ge 2 ] || usage
      case "$1" in
        --branch) BRANCH="$2" ;;
        --head-ref) HEAD_REF="$2" ;;
        --config) CONFIG_OPT="$2" ;;
        --manifest) MANIFEST_OPT="$2" ;;
      esac
      shift 2 ;;
    --branch=*) BRANCH="${1#--branch=}"; shift ;;
    --head-ref=*) HEAD_REF="${1#--head-ref=}"; shift ;;
    --config=*) CONFIG_OPT="${1#--config=}"; shift ;;
    --manifest=*) MANIFEST_OPT="${1#--manifest=}"; shift ;;
    -h|--help) usage ;;
    -*) echo "error: unknown flag: $1" >&2; usage ;;
    *) ARGS+=("$1"); shift ;;
  esac
done
[ "${#ARGS[@]}" -eq 2 ] || usage
BASE="${ARGS[0]}"
HEAD="${ARGS[1]}"
[ -n "$BRANCH" ] || BRANCH="${BASE#origin/}"
[ -n "$HEAD_REF" ] || HEAD_REF="${HEAD#origin/}"

command -v jq >/dev/null 2>&1 || die "jq is required but not installed"
cd "$(git rev-parse --show-toplevel)"

git rev-parse -q --verify "$BASE^{commit}" >/dev/null || die "unknown revision: $BASE"
git rev-parse -q --verify "$HEAD^{commit}" >/dev/null || die "unknown revision: $HEAD"
FROM=$(git merge-base "$BASE" "$HEAD") || die "$BASE and $HEAD share no history"

if [ -n "$CONFIG_OPT" ]; then
  CONFIG_PATH="$CONFIG_OPT"
  git cat-file -e "$FROM:$CONFIG_PATH" 2>/dev/null || die "no $CONFIG_PATH at $FROM"
elif ! CONFIG_PATH=$(rp_config_path "$FROM"); then
  echo "no release-please config at $FROM; nothing to check"
  exit 0
fi
MANIFEST_PATH="${MANIFEST_OPT:-$(rp_manifest_path "$CONFIG_PATH")}"
# The fix commands name the helper (RELEASE_AS_CMD overrides where it lives)
# and carry the paths when they are not the defaults.
HELPER="${RELEASE_AS_CMD:-scripts/release-as.sh}"
[ -z "$CONFIG_OPT" ] || HELPER="$HELPER --config $CONFIG_PATH"
[ -z "$MANIFEST_OPT" ] || HELPER="$HELPER --manifest $MANIFEST_PATH"
CONFIG=$(rp_show "$FROM" "$CONFIG_PATH")
BASE_MANIFEST=$(rp_show "$FROM" "$MANIFEST_PATH") ||
  die "no $MANIFEST_PATH at $FROM"
HEAD_MANIFEST=$(rp_show "$HEAD" "$MANIFEST_PATH") ||
  die "no $MANIFEST_PATH at $HEAD"

# Packages this PR releases: manifest entries that moved, known to the
# config, and the packages its head branch names.
NAMED=()
case "$HEAD_REF" in
  release-please--branches--*--components--*)
    NAMED+=("$(rp_component_path "$CONFIG" "${HEAD_REF##*--components--}")") ;;
  release-please--branches--*--groups--*)
    while IFS= read -r m; do
      [ -n "$m" ] && NAMED+=("$(rp_component_path "$CONFIG" "$m")")
    done < <(rp_group_members "$CONFIG" "${HEAD_REF##*--groups--}") ;;
esac
RELEASED=$( {
  jq -r -n --argjson base "$BASE_MANIFEST" --argjson head "$HEAD_MANIFEST" \
    --argjson cfg "$CONFIG" '
      $head | to_entries[]
      | select($cfg.packages[.key] != null and $base[.key] != .value)
      | .key'
  [ "${#NAMED[@]}" -eq 0 ] || printf '%s\n' "${NAMED[@]}"
} | awk 'NF && !seen[$0]++')

if [ -z "$RELEASED" ]; then
  echo "no manifest version changes between $FROM and $HEAD; nothing to check"
  exit 0
fi

current_of() { jq -r --arg p "$1" '.[$p] // empty' <<<"$BASE_MANIFEST"; }
proposed_of() { jq -r --arg p "$1" '.[$p] // empty' <<<"$HEAD_MANIFEST"; }

# group_fix <group> <version> — the linked release every member can take:
# the highest of <version> and each member's next minor (major) line.
group_fix() {
  local m p c versions=("$2")
  while IFS= read -r m; do
    p=$(rp_component_path "$CONFIG" "$m")
    c=$(current_of "$p")
    [ -n "$c" ] && versions+=("$(rp_new_line "$c")")
  done < <(rp_group_members "$CONFIG" "$1")
  rp_semver_max "${versions[@]}"
}

FAILED=0

# pins: the PR still carries pins it cuts, or a plugin entry with no pin left.
# The pins job drops them on the PR branch, and its push runs this check
# again; merged before that, they stay on the base branch.
HEAD_CONFIG=$(rp_show "$HEAD" "$CONFIG_PATH") || die "no $CONFIG_PATH at $HEAD"
PLAN=$(rp_cleanup_plan "$HEAD_CONFIG" "$HEAD_MANIFEST")
if [ -n "$PLAN" ]; then
  {
    echo "Waiting for the pins job: the PR's config still holds what its release cuts:"
    while IFS=$'\t' read -r kind p v; do
      case "$kind" in
        pin) echo "  $(rp_component "$HEAD_CONFIG" "$p"): release-as $v, reached by the PR's manifest ($(proposed_of "$p"))" ;;
        plugin) echo "  the linked-versions plugin entry of group $p, whose members keep no pin" ;;
      esac
    done <<<"$PLAN"
    echo "Merged as is, they stay on $BRANCH, and the next release PR proposes the"
    echo "same versions again. The pins job drops them on the PR branch, and its push"
    echo "runs this check again. If that job failed, re-run it, or run this on the"
    echo "PR branch and push:"
    echo ""
    echo "  $HELPER --cleanup"
  } >&2
  fail "Waiting for the pins job" "the PR still carries the release-as pins it cuts; the pins job drops them, or run $HELPER --cleanup on the PR branch"
fi

while IFS= read -r path; do
  component=$(rp_component "$CONFIG" "$path")
  [ -n "$component" ] || die "package \"$path\" has no component in $CONFIG_PATH"
  current=$(current_of "$path")
  proposed=$(proposed_of "$path")
  rp_semver_valid "$proposed" || die "$component: $proposed in $HEAD is not semver"
  group=$(rp_group_of "$CONFIG" "$component")

  # stale: an existing tag, or a pin release-please already cut.
  proposed_tag=$(rp_tag "$CONFIG" "$path" "$proposed")
  pin=$(rp_release_as "$CONFIG" "$path")
  stale=""
  if git rev-parse -q --verify "refs/tags/$proposed_tag^{commit}" >/dev/null; then
    stale="$proposed_tag already exists"
  fi
  if [ -n "$pin" ] && [ -n "$current" ] && [ "$(rp_semver_cmp "$pin" "$current")" -le 0 ]; then
    stale="${stale:+$stale; }its release-as pin $pin is not newer than $current"
  fi
  if [ -n "$stale" ]; then
    {
      echo "$component: proposes $proposed, but $stale."
      echo "Fix: on a branch off an up-to-date $BRANCH, drop the pins the"
      echo "manifest has reached:"
      echo ""
      echo "  $HELPER --cleanup"
      echo ""
      echo "With no pin left, a Release-As footer a merge replayed (a next -> main"
      echo "promotion, say) re-proposes it; pin the version you want instead,"
      echo "which release-please prefers over footers:"
      echo ""
      echo "  $HELPER $component <version>"
    } >&2
    fail "Stale release-as" "$component $proposed: $stale; run $HELPER --cleanup on a branch off $BRANCH"
    continue
  fi

  if [ -z "$current" ]; then
    echo "$component: first release ($proposed); nothing to compare"
    continue
  fi
  rp_semver_valid "$current" || die "$component: $current at $FROM is not semver"

  tag=$(rp_tag "$CONFIG" "$path" "$current")
  git rev-parse -q --verify "refs/tags/$tag^{commit}" >/dev/null ||
    die "$component: tag $tag (the release of $current) not found;" \
      "fetch tags (actions/checkout needs fetch-depth: 0)"

  breaking=()
  while IFS= read -r sha; do
    [ -n "$sha" ] || continue
    rp_is_breaking "$(git log -1 --format=%B "$sha")" || continue
    if rp_route "$CONFIG" "$(rp_commit_files "$sha")" | grep -qxF -- "$path"; then
      breaking+=("$(git log -1 --format='%h %s' "$sha")")
    fi
  done < <(git rev-list --no-merges "$tag..$HEAD")

  want=""
  if [ "${#breaking[@]}" -gt 0 ] && ! rp_is_new_line "$current" "$proposed"; then
    want=$(rp_new_line "$current")
  fi

  missing=""
  linked=false
  if [ -n "$group" ] && rp_linked_applicable "$current" "${want:-$proposed}"; then
    linked=true
    while IFS= read -r m; do
      mp=$(rp_component_path "$CONFIG" "$m")
      if [ -z "$mp" ] || [ "$(proposed_of "$mp")" != "$proposed" ] ||
         ! grep -qxF -- "$mp" <<<"$RELEASED"; then
        missing="${missing:+$missing, }$m"
      fi
    done < <(rp_group_members "$CONFIG" "$group")
  fi

  if [ -z "$want" ] && [ -z "$missing" ]; then
    if [ "${#breaking[@]}" -gt 0 ]; then
      echo "$component: $current -> $proposed starts a new line;" \
        "${#breaking[@]} breaking change(s) since $tag"
    else
      echo "$component: $current -> $proposed, no breaking change since $tag"
    fi
    $linked && echo "$component: linked release of group $group; every member at $proposed"
    continue
  fi

  if [ -n "$group" ] && $linked; then
    fix_version=$(group_fix "$group" "${want:-$proposed}")
    fix="$HELPER --group $group $fix_version"
  else
    fix_version="$want"
    fix="$HELPER $component $want"
  fi
  {
    if [ -n "$want" ]; then
      echo "$component: $current -> $proposed keeps the line, but" \
        "${#breaking[@]} breaking commit(s) since $tag touch $component:"
      printf '  %s\n' "${breaking[@]}"
      echo "A breaking change starts a new minor line: $component $want."
    fi
    if $linked; then
      echo "$component: ${want:-$proposed} is a linked release of group $group" \
        "(a new major, or a new minor in pre-release), so every member"
      echo "releases at $fix_version in one PR; ${missing:-no member} does not here."
    fi
    echo "Fix: on a branch off an up-to-date $BRANCH, run"
    echo ""
    echo "  $fix"
    echo ""
    echo "and merge that commit into $BRANCH; release-please then rebuilds the"
    echo "release PR at $fix_version."
  } >&2
  if [ -n "$want" ]; then
    fail "Breaking change needs a new minor line" "$component $proposed keeps the line of $current although ${#breaking[@]} breaking commit(s) touch it; run $fix on a branch off $BRANCH"
  else
    fail "Linked release needs the whole group" "$component $proposed is a linked release of group $group but ${missing} do(es) not release at it; run $fix on a branch off $BRANCH"
  fi
done <<<"$RELEASED"

exit "$FAILED"
