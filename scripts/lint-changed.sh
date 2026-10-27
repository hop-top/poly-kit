#!/usr/bin/env bash
#
# lint-changed.sh — run a linter on the files a change touches, or on
# everything when the change calls for it.
#
# The changed set comes from scripts/changed-files.sh (base, LINT_ALL,
# LINT_BASE, LINT_HEAD: see its header). Three outcomes:
#
#   nothing relevant changed   print a skip line, exit 0
#   files changed              run CMD with the changed files appended
#                              (paths relative to --cd DIR)
#   lint everything            run CMD with the --full arguments appended:
#                              LINT_ALL set, a --trigger path changed, the
#                              changed-files helper changed, or no base
#
# Usage:
#   scripts/lint-changed.sh NAME [options] [pathspec...] -- CMD [ARG...]
#
#   NAME             label for the log lines (the make target)
#   --cd DIR         run CMD inside DIR; pathspecs stay repo-relative and
#                    should select files under DIR
#   --full ARG       argument appended to CMD in a full run instead of the
#                    file list (repeatable; none: CMD runs bare)
#   --gate           package-level tool (tsc, clippy, phpstan): run CMD
#                    bare, once, when any matching path changed
#   --trigger SPEC   passed to changed-files.sh: a change here lints all
#   --include-deleted
#                    passed to changed-files.sh: a deletion counts as a
#                    change (with --gate: a removed file can break a build)
#   pathspec         git pathspecs selecting the files CMD lints
#
# Exit: CMD's status; 0 on skip; changed-files.sh's status on its errors.
set -euo pipefail

[ $# -ge 1 ] || { echo "usage: lint-changed.sh NAME [options] [pathspec...] -- CMD [ARG...]" >&2; exit 2; }
name=$1; shift

dir=.
gate=0
full_args=()
cf_args=()
specs=()
while [ $# -gt 0 ]; do
    case "$1" in
        --cd) dir=$2; shift 2 ;;
        --full) full_args+=("$2"); shift 2 ;;
        --gate) gate=1; shift ;;
        --trigger) cf_args+=(--trigger "$2"); shift 2 ;;
        --include-deleted) cf_args+=(--include-deleted); shift ;;
        --) shift; break ;;
        -*) echo "lint-changed: unknown option: $1" >&2; exit 2 ;;
        *) specs+=("$1"); shift ;;
    esac
done
[ $# -ge 1 ] || { echo "lint-changed: $name: no command after --" >&2; exit 2; }

cd "$(git rev-parse --show-toplevel)"
here=$(dirname "$0")

rc=0
files=$("$here/changed-files.sh" ${cf_args[@]+"${cf_args[@]}"} -- ${specs[@]+"${specs[@]}"}) || rc=$?
case "$rc" in
    0) ;;
    3)
        echo "==> $name: full run"
        cd "$dir"
        exec "$@" ${full_args[@]+"${full_args[@]}"}
        ;;
    *) exit "$rc" ;;
esac

if [ -z "$files" ]; then
    echo "==> $name: no relevant changes, skipped (LINT_ALL=1 lints everything)"
    exit 0
fi

cd "$dir"
if [ "$gate" -eq 1 ]; then
    echo "==> $name: $(printf '%s\n' "$files" | wc -l | tr -d ' ') relevant file(s) changed"
    exec "$@"
fi

rel=()
while IFS= read -r f; do
    if [ "$dir" = . ]; then rel+=("$f"); else rel+=("${f#"${dir%/}"/}"); fi
done <<<"$files"
echo "==> $name: ${#rel[@]} changed file(s)"
exec "$@" "${rel[@]}"
