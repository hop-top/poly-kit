#!/usr/bin/env bash
set -euo pipefail

# Rewrite raw release-please changelog into polished format.
# Idempotent — detects prior rewrite via "team is happy" marker.

MARKER="team is happy to announce"
FILE=""
COMPONENT=""
REPO=""
DRY_RUN=false

die() { echo "error: $*" >&2; exit 1; }

usage() {
  cat <<EOF
Usage: $(basename "$0") --file <path> --component <name> [--repo <owner/repo>]
EOF
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --file)      FILE="$2"; shift 2 ;;
    --component) COMPONENT="$2"; shift 2 ;;
    --repo)      REPO="$2"; shift 2 ;;
    --dry-run)   DRY_RUN=true; shift ;;
    -h|--help)   usage ;;
    *)           die "unknown flag: $1" ;;
  esac
done

[[ -n "$FILE" ]]      || die "--file is required"
[[ -f "$FILE" ]]      || die "file not found: $FILE"
[[ -n "$COMPONENT" ]] || die "--component is required"

if [[ -z "$REPO" ]]; then
  REPO=$(git remote get-url origin 2>/dev/null \
    | sed -E 's#.*github\.com[:/]##; s#\.git$##') \
    || die "cannot detect repo from git remote"
fi

# --- Locate latest release header ---

header=$(grep -n '^## \[' "$FILE" | head -1) \
  || die "no version header found in $FILE"
header_line=$(echo "$header" | cut -d: -f1)
header_text=$(sed -n "${header_line}p" "$FILE")

# idempotent check — marker may be on line+1 or line+2 (blank line between)
if sed -n "$((header_line + 1)),$((header_line + 3))p" "$FILE" \
     | grep -q "$MARKER"; then
  echo "already rewritten: $FILE"
  exit 0
fi

# parse version + compare URL from header
version=$(echo "$header_text" \
  | sed -E 's/^## \[([^]]+)\].*/\1/')
compare_url=$(echo "$header_text" \
  | sed -E 's/^## \[[^]]+\]\(([^)]+)\).*/\1/')

# base..head ref from compare URL
compare_ref=$(echo "$compare_url" \
  | sed -E 's#.*/compare/(.*)#\1#')

# --- Find end of latest entry ---

second_header=$(grep -n '^## \[' "$FILE" | sed -n '2p')
if [[ -n "$second_header" ]]; then
  end_line=$(($(echo "$second_header" | cut -d: -f1) - 1))
else
  end_line=$(wc -l < "$FILE" | tr -d ' ')
fi

# --- Extract entry and after sections ---

entry=$(sed -n "$((header_line + 1)),${end_line}p" "$FILE")
after=$(sed -n "$((end_line + 1)),\$p" "$FILE" 2>/dev/null) || after=""

# --- Determine release type ---

has_features=false
has_fixes=false
has_perf=false

echo "$entry" | grep -q '^### Features'    && has_features=true
echo "$entry" | grep -q '^### Bug Fixes'   && has_fixes=true
echo "$entry" | grep -q '^### Performance' && has_perf=true

if $has_features && $has_fixes; then
  release_type="new features and bug fixes"
elif $has_features && $has_perf; then
  release_type="new features and performance improvements"
elif $has_features; then
  release_type="new features"
elif $has_fixes && $has_perf; then
  release_type="bug fixes and performance improvements"
elif $has_fixes; then
  release_type="maintenance release with bug fixes"
elif $has_perf; then
  release_type="performance improvements"
else
  release_type="miscellaneous improvements"
fi

# --- Strip SHAs and PR links (entry only) ---

entry=$(echo "$entry" \
  | sed -E 's/( \(\[#[0-9]+\]\([^)]+\)\))?( \((\[)?[a-f0-9]{7,}(\]\([^)]+\))?\))?$//')

# trim trailing blank lines from entry
entry=$(echo "$entry" | awk '
  /^[[:space:]]*$/ { blank = blank ORS; next }
  { if (NR > 1 && blank != "") printf "%s", blank; blank = ""; print }
')

# --- Fetch contributors ---

contributors=""
if command -v gh >/dev/null 2>&1; then
  LOGIN_TMP=$(mktemp)
  trap "rm -f '$LOGIN_TMP'" EXIT
  logins=""
  if gh api "repos/${REPO}/compare/${compare_ref}" \
       --jq '[.commits[].author.login // empty] | unique | .[]' \
       > "$LOGIN_TMP" 2>/dev/null; then
    logins=$(cat "$LOGIN_TMP")
  fi
  rm -f "$LOGIN_TMP"

  if [[ -n "$logins" ]]; then
    while IFS= read -r login; do
      [[ -z "$login" ]] && continue
      name=$(gh api "users/${login}" \
        --jq '.name // empty' 2>/dev/null) || true
      if [[ -n "$name" ]]; then
        contributors="${contributors}* ${name} (@${login})"$'\n'
      else
        contributors="${contributors}* @${login}"$'\n'
      fi
    done <<< "$logins"
  fi
fi

# --- Build intro ---

intro="The hop-top team is happy to announce ${COMPONENT} ${version}."
intro="${intro} This release includes ${release_type}."

# --- Reassemble file ---

tmp=$(mktemp -p "$(dirname "$FILE")")
{
  # preamble — preserved verbatim including trailing blank lines
  if [[ "$header_line" -gt 1 ]]; then
    head -n "$((header_line - 1))" "$FILE"
  fi

  # version header
  echo "$header_text"
  echo ""
  echo "$intro"

  # entry body (sections + bullets, SHAs stripped)
  echo "$entry"

  # contributors
  if [[ -n "$contributors" ]]; then
    echo ""
    echo "### Contributors"
    echo ""
    printf "%s" "$contributors"
  fi

  # diff link
  echo ""
  echo "Full diff: [${compare_ref}](${compare_url})"

  # rest of changelog (older entries)
  if [[ -n "$after" ]]; then
    echo ""
    echo "$after"
  fi
} > "$tmp"

if $DRY_RUN; then
  echo "[dry-run] Would rewrite $FILE"
  echo "[dry-run] Version: $version"
  echo "[dry-run] Release type: $release_type"
  echo "[dry-run] Intro: \"$intro\""
  sha_count=$(diff "$FILE" "$tmp" \
    | grep -c '^[<>].*([a-f0-9]\{7,\})' 2>/dev/null) || sha_count=0
  echo "[dry-run] Would strip SHAs from $sha_count bullet points"
  if [[ -n "$contributors" ]]; then
    contrib_count=$(echo "$contributors" | grep -c '^\*') || contrib_count=0
    echo "[dry-run] Would add Contributors section ($contrib_count contributors)"
  fi
  echo "[dry-run] Would add Full diff link"
  echo "[dry-run] Preview:"
  diff -u "$FILE" "$tmp" \
    | sed "s|--- $FILE|--- $FILE (before)|;s|+++ $tmp|+++ $FILE (after)|" \
    || true
  echo "[dry-run] No changes made."
  rm -f "$tmp"
  exit 0
fi

mv "$tmp" "$FILE"
echo "rewritten: $FILE"
