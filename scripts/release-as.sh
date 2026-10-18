#!/usr/bin/env bash
#
# release-as.sh — pin the version release-please proposes next, for one
# component or a linked group, and drop the pins once a release cuts them.
#
# A pin is release-please's per-package "release-as" field in the config:
# release-please proposes exactly that version for that package and no
# other, whatever the commits say. A linked group (the config's own
# "x-linked-groups" key) is pinned member by member, and its release-please
# "linked-versions" plugin is wired in for that release only: it releases
# every member in one pull request, members without commits included. The
# release PR drops what it cuts (`--cleanup`, run by changelog-rewrite.yml),
# so merging it leaves the config as it was. See RELEASING.md.
#
# A pin alone does not open a release PR for a package with no commits since
# its release (or hidden types only). The commit carries a Release-As footer
# with the pinned version when it pins the one package the config file
# itself reaches (the root package), which makes the commit count for it; a
# linked group's plugin then adds the members without commits. Any other
# package's pin waits for its next commit, and the script says so.
#
# Edits are line-based so the config keeps its formatting: a pin is one
# "release-as" line after the package's "component" line, and "plugins" one
# line before "packages". After each edit the script checks with jq that the
# config parses and that nothing else changed. It commits the config alone
# and never pushes.
#
# Usage:
#   scripts/release-as.sh [<options>] <component> <version>
#   scripts/release-as.sh [<options>] --group <group> <version>
#   scripts/release-as.sh [<options>] --cleanup
#
#   options: --dry-run, --config <path>, --manifest <path>
#
#   scripts/release-as.sh qmochi 0.3.0-alpha.0
#   scripts/release-as.sh --group kit 0.6.0-alpha.0
#
# A pin is refused when the version is not newer than the manifest's, when
# the package already carries it, or, for a linked group's member, when the
# version would make a linked release (use --group). --cleanup drops every
# pin the manifest has reached and every linked-versions plugin entry whose
# group no longer has a pin; with nothing to drop it does nothing.
# --dry-run prints the config diff without writing or committing. --config
# defaults to .github/release-please-config.json, else
# release-please-config.json, and --manifest to .release-please-manifest.json
# next to the config.
#
# Exit: 0 done (or nothing to clean up, or dry run), 1 refused, 2 usage.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/release-lib.sh
. "$SCRIPT_DIR/release-lib.sh"

WORK="" MSG=""
trap '[ -z "$WORK" ] || rm -f "$WORK" "$WORK.next"; [ -z "$MSG" ] || rm -f "$MSG"' EXIT

die() { echo "error: $*" >&2; exit 1; }

usage() {
  sed -n '/^# Usage:/,/^# Exit:/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
  exit 2
}

# Line-based edits of a config written one key per line. depth = containers
# open before the line; cur = the package whose block the line sits in.
#   op=pin      set packages[pkg]["release-as"] to val (has: it exists)
#   op=unpin    delete packages[pkg]["release-as"]
#   op=plugins  set the one-line "plugins" value to val, or delete it when
#               val is empty (has: the key exists); exit 3 if it spans lines
# Exit 0 when the edit applied.
# shellcheck disable=SC2016 # an awk program: its $ fields are awk's
EDIT_AWK='
function delta(s,   i, c, q, e, n) {
  n = 0; q = 0; e = 0
  for (i = 1; i <= length(s); i++) {
    c = substr(s, i, 1)
    if (q) { if (e) e = 0; else if (c == "\\") e = 1; else if (c == "\"") q = 0; continue }
    if (c == "\"") q = 1
    else if (c == "{" || c == "[") n++
    else if (c == "}" || c == "]") n--
  }
  return n
}
function key(s) {
  if (!match(s, /^[[:space:]]*"[^"]*"[[:space:]]*:/)) return ""
  s = substr(s, RSTART, RLENGTH); sub(/^[[:space:]]*"/, "", s); sub(/"[[:space:]]*:$/, "", s)
  return s
}
function indent(s) { match(s, /^[[:space:]]*/); return substr(s, 1, RLENGTH) }
function emit(s) { if (have) print held; held = s; have = 1 }
BEGIN { status = 1 }
{
  line = $0; d = depth; k = key(line)
  if (d == 1 && k != "") inpk = (k == "packages")
  if (inpk && d == 2 && k != "") cur = k
  depth += delta(line)
  if (op == "pin" && inpk && d == 3 && cur == pkg) {
    if (k == "release-as") {
      sub(/"release-as"[[:space:]]*:[[:space:]]*"[^"]*"/, "\"release-as\": \"" val "\"", line)
      status = 0
    } else if (k == "component" && !has) {
      comma = (line ~ /,[[:space:]]*$/)
      if (!comma) sub(/[[:space:]]*$/, ",", line)
      emit(line)
      line = indent(line) "\"release-as\": \"" val "\"" (comma ? "," : "")
      status = 0
    }
  } else if (op == "unpin" && inpk && d == 3 && cur == pkg && k == "release-as") {
    if (line !~ /,[[:space:]]*$/ && have) sub(/,[[:space:]]*$/, "", held)
    status = 0
    next
  } else if (op == "plugins" && d == 1 && k == "plugins") {
    if (delta(line) != 0) { status = 3; exit }
    comma = (line ~ /,[[:space:]]*$/)
    if (val == "") {
      if (!comma && have) sub(/,[[:space:]]*$/, "", held)
      status = 0
      next
    }
    line = indent(line) "\"plugins\": " val (comma ? "," : "")
    status = 0
  } else if (op == "plugins" && d == 1 && k == "packages" && !has && val != "") {
    emit(indent(line) "\"plugins\": " val ",")
    status = 0
  }
  emit(line)
}
END { if (status == 3) exit 3; if (have) print held; exit status }
'

# edit <op> <pkg> <val> <has> — apply one edit to $WORK, then check with jq
# that the result parses and differs from $WORK only where the edit says.
edit() {
  local op="$1" pkg="$2" val="$3" has="$4" rc=0 scope
  awk -v op="$op" -v pkg="$pkg" -v val="$val" -v has="$has" "$EDIT_AWK" \
    "$WORK" > "$WORK.next" || rc=$?
  case "$rc" in
    0) ;;
    3) die "$CONFIG_PATH: \"plugins\" spans several lines; edit it by hand" ;;
    *) die "$CONFIG_PATH: no line to anchor the $op edit for \"$pkg\" on" \
         "(each package needs a \"component\" line)" ;;
  esac
  jq -e . "$WORK.next" >/dev/null 2>&1 || die "edit produced invalid JSON; nothing written"
  case "$op" in
    plugins) scope='del(.plugins)' ;;
    *) scope="del(.packages[\$p][\"release-as\"])" ;;
  esac
  [ "$(jq -S --arg p "$pkg" "$scope" "$WORK")" = "$(jq -S --arg p "$pkg" "$scope" "$WORK.next")" ] ||
    die "edit changed more than intended; nothing written"
  mv "$WORK.next" "$WORK"
}

# pin <path> <version> — set the package's release-as in $WORK.
pin() {
  local has
  has=$(jq -r --arg p "$1" '.packages[$p] | has("release-as")' "$WORK")
  edit pin "$1" "$2" "$([ "$has" = true ] && echo 1 || echo 0)"
  [ "$(jq -r --arg p "$1" '.packages[$p]["release-as"]' "$WORK")" = "$2" ] ||
    die "could not pin $1 to $2; nothing written"
}

# set_plugins <json-array> — make "plugins" hold the array, one line; an
# empty array removes the key.
set_plugins() {
  local has rendered=""
  has=$(jq -r 'has("plugins")' "$WORK")
  if [ "$(jq 'length' <<<"$1")" -gt 0 ]; then
    rendered=$(jq -r '
      def render: if type == "object"
        then "{" + ([to_entries[] | "\(.key | tojson): \(.value | render)"] | join(", ")) + "}"
        elif type == "array" then "[" + (map(render) | join(", ")) + "]"
        else tojson end;
      render' <<<"$1")
  elif [ "$has" != true ]; then
    return 0
  fi
  edit plugins "" "$rendered" "$([ "$has" = true ] && echo 1 || echo 0)"
  [ "$(jq -c '.plugins // []' "$WORK")" = "$(jq -c . <<<"$1")" ] ||
    die "could not set plugins; nothing written"
}

manifest_version() {
  jq -r --arg p "$1" '.[$p] // empty' "$MANIFEST_PATH" 2>/dev/null || true
}

# newer <component> <path> <version> — refuse a version not above the
# manifest's.
newer() {
  local current
  current=$(manifest_version "$2")
  if [ -n "$current" ] && [ "$(rp_semver_cmp "$3" "$current")" -le 0 ]; then
    die "$1 is at $current in $MANIFEST_PATH; $3 is not newer"
  fi
}

MODE=pin DRY_RUN=false CONFIG_OPT="" MANIFEST_OPT=""
ARGS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN=true ;;
    --group) MODE=group ;;
    --cleanup) MODE=cleanup ;;
    --config|--manifest)
      [ $# -ge 2 ] || usage
      if [ "$1" = --config ]; then CONFIG_OPT="$2"; else MANIFEST_OPT="$2"; fi
      shift ;;
    --config=*) CONFIG_OPT="${1#--config=}" ;;
    --manifest=*) MANIFEST_OPT="${1#--manifest=}" ;;
    -h|--help) usage ;;
    -*) echo "error: unknown flag: $1" >&2; usage ;;
    *) ARGS+=("$1") ;;
  esac
  shift
done
case "$MODE" in
  cleanup) [ "${#ARGS[@]}" -eq 0 ] || usage ;;
  *) [ "${#ARGS[@]}" -eq 2 ] || usage ;;
esac

command -v jq >/dev/null 2>&1 || die "jq is required but not installed"
cd "$(git rev-parse --show-toplevel)"

if [ -n "$CONFIG_OPT" ]; then
  CONFIG_PATH="$CONFIG_OPT"
  [ -f "$CONFIG_PATH" ] || die "no $CONFIG_PATH"
else
  CONFIG_PATH=$(rp_config_path "") ||
    die "release-please-config.json not found under .github/ or the repo root"
fi
MANIFEST_PATH="${MANIFEST_OPT:-$(rp_manifest_path "$CONFIG_PATH")}"
CONFIG=$(cat "$CONFIG_PATH")
WORK=$(mktemp)
cp "$CONFIG_PATH" "$WORK"

if [ "$MODE" != cleanup ]; then
  VERSION="${ARGS[1]}"
  case "$VERSION" in
    v[0-9]*) die "version \"$VERSION\": drop the leading v (release-as takes a bare semver)" ;;
  esac
  rp_semver_valid "$VERSION" || die "version \"$VERSION\" is not semver (x.y.z[-pre])"
  VERB="start"
  case "$VERSION" in *-*) ;; *) VERB="cut" ;; esac
fi

case "$MODE" in
  pin)
    COMPONENT="${ARGS[0]}"
    PKG=$(rp_component_path "$CONFIG" "$COMPONENT")
    [ -n "$PKG" ] ||
      die "unknown component \"$COMPONENT\"; $CONFIG_PATH has:" \
        "$(rp_components "$CONFIG" | paste -sd' ' -)"
    newer "$COMPONENT" "$PKG" "$VERSION"
    GROUP=$(rp_group_of "$CONFIG" "$COMPONENT")
    CURRENT=$(manifest_version "$PKG")
    if [ -n "$GROUP" ] && [ -n "$CURRENT" ] && rp_linked_applicable "$CURRENT" "$VERSION"; then
      die "$COMPONENT $CURRENT -> $VERSION is a linked release for group $GROUP;" \
        "pin the whole group: ${RELEASE_AS_CMD:-scripts/release-as.sh} --group $GROUP $VERSION"
    fi
    [ "$(rp_release_as "$CONFIG" "$PKG")" != "$VERSION" ] ||
      die "$COMPONENT is already pinned to $VERSION; nothing written"
    pin "$PKG" "$VERSION"
    PINNED="$PKG"
    SUBJECT="chore(release): $VERB $COMPONENT $VERSION"
    BODY=""
    EXPECT="\"chore(release): $COMPONENT $VERSION\""
    ;;
  group)
    GROUP="${ARGS[0]}"
    MEMBERS=$(rp_group_members "$CONFIG" "$GROUP")
    [ -n "$MEMBERS" ] ||
      die "unknown linked group \"$GROUP\"; x-linked-groups in $CONFIG_PATH has:" \
        "$(jq -r '[.["x-linked-groups"] // [] | .[].group] | join(" ")' <<<"$CONFIG")"
    PINNED=""
    while IFS= read -r m; do
      p=$(rp_component_path "$CONFIG" "$m")
      [ -n "$p" ] || die "group $GROUP lists \"$m\", which no package has as its component"
      newer "$m" "$p" "$VERSION"
      pin "$p" "$VERSION"
      PINNED="$PINNED$p"$'\n'
    done <<<"$MEMBERS"
    set_plugins "$(jq -c --arg g "$GROUP" --argjson m "$(jq -R . <<<"$MEMBERS" | jq -sc .)" '
      (.plugins // []) | map(select(type != "object" or .type != "linked-versions" or .groupName != $g))
      + [{type: "linked-versions", groupName: $g, components: $m}]' "$WORK")"
    cmp -s "$WORK" "$CONFIG_PATH" &&
      die "group $GROUP is already pinned to $VERSION; nothing written"
    SUBJECT="chore(release): $VERB group $GROUP $VERSION"
    BODY="release-as $VERSION on $(paste -sd, - <<<"$MEMBERS" | sed 's/,/, /g');
linked-versions plugin for $GROUP until the release PR drops both"
    EXPECT="the group PR \"chore(<target-branch>): release $GROUP libraries\" (every
member at $VERSION in its body)"
    ;;
  cleanup)
    # The same plan check-release-bump.sh holds a release PR to.
    DROPPED=() GONE=""
    PLAN=$(rp_cleanup_plan "$CONFIG" "$(cat "$MANIFEST_PATH" 2>/dev/null || echo '{}')")
    while IFS=$'\t' read -r kind p v; do
      case "$kind" in
        pin)
          edit unpin "$p" "" 1
          DROPPED+=("$(rp_component "$CONFIG" "$p") $v") ;;
        plugin)
          GONE="$GONE$p"$'\n'
          DROPPED+=("linked-versions plugin for $p") ;;
      esac
    done <<<"$PLAN"
    set_plugins "$(jq -c --arg gone "$GONE" '($gone | split("\n")) as $gone
      | (.plugins // []) | map(select(type != "object" or .type != "linked-versions"
          or (.groupName as $g | $gone | index($g) | not)))' "$WORK")"
    if cmp -s "$WORK" "$CONFIG_PATH"; then
      echo "no release-as pin the manifest has reached; nothing to drop"
      exit 0
    fi
    SUBJECT="chore(release): drop release-as pins this release cuts"
    BODY=$(printf '%s\n' "${DROPPED[@]}" | paste -sd, - | sed 's/,/, /g')
    ;;
esac

# A pin opens no release PR by itself for a package whose commits since its
# release are none, or only hidden types: release-please skips it before it
# reads the pin. This commit touches only the config file, which reaches one
# package (the root one, here). When the commit pins that package, a
# Release-As footer with the same version makes the commit count for it, so
# its PR opens; for a linked group the plugin then adds the members without
# commits. A package the config file does not reach waits for its next
# commit of a visible type.
FOOTER=""
if [ "$MODE" != cleanup ]; then
  REACH=$(rp_route "$CONFIG" "$(jq -cn --arg f "$CONFIG_PATH" '[$f]')")
  if [ -n "$REACH" ] && [ "$(grep -c . <<<"$REACH")" -eq 1 ] &&
     grep -qxF -- "$REACH" <<<"$PINNED"; then
    FOOTER="Release-As: $VERSION"
  elif [ "$MODE" = pin ]; then
    current=$(manifest_version "$PKG")
    tag=$( [ -z "$current" ] || rp_tag "$CONFIG" "$PKG" "$current")
    if [ -n "$tag" ] && git rev-parse -q --verify "refs/tags/$tag^{commit}" >/dev/null &&
       [ -z "$(git rev-list -1 "$tag..HEAD" -- "$PKG")" ]; then
      echo "note: no commit has touched $PKG since $tag; release-please opens" \
        "the $COMPONENT PR with the next commit there of a visible type" >&2
    fi
  fi
fi

if $DRY_RUN; then
  echo "[dry-run] would commit: $SUBJECT${FOOTER:+ ($FOOTER)}"
  diff -u "$CONFIG_PATH" "$WORK" | sed '1,2d' || true
  echo "[dry-run] No changes made."
  exit 0
fi

git diff --cached --quiet ||
  die "staged changes detected; commit or unstage them first"

cp "$WORK" "$CONFIG_PATH"
MSG=$(mktemp)
{
  printf '%s\n' "$SUBJECT"
  [ -z "$BODY" ] || printf '\n%s\n' "$BODY"
  [ -z "$FOOTER" ] || printf '\n%s\n' "$FOOTER"
} > "$MSG"
git commit -q -F "$MSG" -- "$CONFIG_PATH"
echo "Committed $(git log -1 --format='%h %s')"
[ "$MODE" != cleanup ] || exit 0

BRANCH=$(git symbolic-ref -q --short HEAD || echo "<branch>")
REPO=$(git remote get-url origin 2>/dev/null |
  sed -E 's#^(git@[^:]+:|[a-z+]+://([^@/]*@)?[^/]+/)##; s#\.git$##') || true
[ -n "$REPO" ] || REPO="<owner>/<repo>"
RP=$("$SCRIPT_DIR/toolchain-pins.sh" release-please 2>/dev/null) || RP=17

cat <<EOF

Before merging, push $BRANCH and dry-run release-please against it:

  npx release-please@$RP release-pr --dry-run \\
    --token "\$(gh auth token)" --repo-url $REPO \\
    --target-branch $BRANCH \\
    --config-file $CONFIG_PATH \\
    --manifest-file $MANIFEST_PATH | grep '^title:'

Expect $EXPECT among the titles, and every other component on the version
it would get without this commit.
EOF
