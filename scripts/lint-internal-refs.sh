#!/usr/bin/env bash
#
# lint-internal-refs.sh — fail when tracked files point at documents or
# paths that live outside this repository.
#
# A reader of the repo cannot open a maintainer's home directory, a
# workspace-private doc, a working note or a design note that was never
# committed. A comment, doc, help string or workflow that cites one
# names something nobody can check. Point at the in-repo doc that holds
# the rule, or state the rationale inline.
#
# Three checks:
#
#   1. Home-directory paths. /Users/<name> and /home/<name> are allowed
#      only for placeholder users (alice, me, testuser, ...), so examples
#      and fixtures stay possible but a real account never lands.
#   2. Names of documents and trees that were never in the repo (see
#      FORBIDDEN below).
#   3. Section and decision citations that name no document (see
#      CITATIONS below). A section mark after the word spec or contract,
#      or a numbered decision, must share its line with a file name
#      (serve-lifecycle.md §"Signals", a Markdown link to the file).
#
# Numbered decision-record mentions are covered by `make lint-adr-refs`.
#
# Patterns write one character as a bracket class (cli[-]conventions)
# so this file never matches itself and needs no exclusion.
#
# Scope: every tracked text file except CHANGELOG.md files, which are
# generated from commit history and never hand-edited.
#
# Usage: scripts/lint-internal-refs.sh   (from anywhere inside the repo)
# Exit:  0 clean, 1 findings, 2 scan error.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# Placeholder account names accepted in /Users/<name> and /home/<name>.
PLACEHOLDERS='alice|bob|carol|me|you|u|x|test|testuser|user|username|runner|config'

# Names of documents and trees that live outside the repo, one ERE each.
FORBIDDEN=(
  # maintainer workspace roots
  '[~]/[.](ops|ops-data|client-data|rlz|agents|w)/'
  '[$]HOME/[.](ops|rlz|w)/'
  'APS[_]DATA_PATH'
  'labspac[e]'
  # conventions and vocabulary docs kept outside the repo
  'cli[-]conventions'
  'glossary[-]event-names'
  # design notes, surveys, specs, plans, stories and audits never committed
  'design([.]md|-note)? [§]'
  '[Ss]urveys? [§]'
  'docs/(specs|audits)/'
  'contributors/(specs|audits|plans|stories)/'
  'kit-init[-]design'
  # tracker names and working notes
  '12fcc[-]dog'
  'safety[-]ladder'
  'Layer-A [t]rack'
  'scaffold[-]emits-'
  'engine-(version-pruning|versioned-branching|store-versioned-sqlite|snapshot-dedup)'
  '[Tt]ask [b]rief|per (the )?task [s]pec'
  # decision records named without a number, or with the number wrapped
  '([Tt]he|[Pp]er|amended) A[D]R'
  'A[D]R *$'
  # the maintainer-only root agent guide
  'AGENTS[.]md(#| [§])'
)

# Citations that must name the document they cite, one ERE each. A
# hit passes when the same line names a file (NAMES_FILE).
CITATIONS=(
  # section of a spec or contract
  '(^|[^A-Za-z])([Ss]pe[c](ification)?s?|[Cc]ontrac[t]s?) §'
  # numbered decision, with or without # (one, several, dashed)
  '(^|[^A-Za-z])[Dd]ecisio[n]s?[ -]#?[0-9]'
)
NAMES_FILE='[A-Za-z0-9_-][.](md|go|proto|ya?ml|json|rs|py|ts|php|sh|toml)([^A-Za-z0-9]|$)'

SCOPE=(-- . ':(exclude,glob)**/CHANGELOG.md')

found=0

# git grep exits 1 on no match and >1 on error; only 0 and 1 are a
# completed scan.
run_grep() {
  local status=0
  git grep -nIE "$@" "${SCOPE[@]}" || status=$?
  if [ "$status" -gt 1 ]; then
    echo "error: git grep failed (exit $status); scan did not run." >&2
    exit 2
  fi
}

homes="$(run_grep '/(Users|home)/[A-Za-z0-9._-]+')"
if [ -n "$homes" ]; then
  # Keep a line only if one of its home paths names a non-placeholder.
  bad_homes="$(printf '%s\n' "$homes" | awk -v ok="^($PLACEHOLDERS)\$" '
    {
      line = $0
      rest = line
      while (match(rest, /\/(Users|home)\/[A-Za-z0-9._-]+/)) {
        seg = substr(rest, RSTART, RLENGTH)
        pre = (RSTART > 1) ? substr(rest, RSTART - 1, 1) : ""
        rest = substr(rest, RSTART + RLENGTH)
        # /x/home/y is a nested path, not a home directory.
        if (pre ~ /[A-Za-z0-9_.-]/) continue
        sub(/^\/(Users|home)\//, "", seg)
        if (seg !~ ok) { print line; break }
      }
    }')"
  if [ -n "$bad_homes" ]; then
    printf '%s\n' "$bad_homes"
    echo "error: home-directory paths above name a real account; use a placeholder user (alice, me, testuser)." >&2
    found=1
  fi
fi

for pat in "${FORBIDDEN[@]}"; do
  hits="$(run_grep "$pat")"
  if [ -n "$hits" ]; then
    printf '%s\n' "$hits"
    found=1
  fi
done

for pat in "${CITATIONS[@]}"; do
  hits="$(run_grep "$pat")"
  [ -n "$hits" ] || continue
  # Match the file name in the line's text, not in the path:line: prefix.
  bare="$(printf '%s\n' "$hits" | awk -v names="$NAMES_FILE" '{
    text = $0
    sub(/^[^:]*:[0-9]+:/, "", text)
    if (text !~ names) print
  }')"
  if [ -n "$bare" ]; then
    printf '%s\n' "$bare"
    echo "error: section or decision citations above name no document; name the in-repo file on the same line, or state the rule inline." >&2
    found=1
  fi
done

if [ "$found" -ne 0 ]; then
  echo "error: references to documents or paths outside the repo found above." >&2
  echo "       Point at the in-repo doc that holds the rule, or state the rationale inline." >&2
  exit 1
fi
echo "No references to out-of-repo documents or paths found."
