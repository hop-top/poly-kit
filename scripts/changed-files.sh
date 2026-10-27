#!/usr/bin/env bash
#
# changed-files.sh — the files a change touches, for linters that run on
# the changed set instead of the whole tree.
#
# One definition of "changed", shared by the Makefile lint targets, the
# pre-push hook and CI, so all three lint the same files.
#
# Base (what the change is measured against), first match wins:
#
#   LINT_BASE=<rev>   explicit base. CI passes the pull request's base SHA
#                     or a push's `before` SHA; the pre-push hook passes
#                     the remote SHA. Diffed from merge-base(<rev>, HEAD)
#                     when history allows, else from <rev> itself.
#   LINT_BASE_REF=<ref>  merge-base of HEAD with <ref>.
#   default           the nearest merge-base of HEAD with origin/next,
#                     origin/main and origin/HEAD (the most recent one,
#                     so a branch cut from next is measured against next).
#
# Compared against (the "head" side):
#
#   LINT_HEAD unset   the working tree: committed, staged and unstaged
#                     changes, plus untracked files not ignored.
#   LINT_HEAD=<rev>   that commit only (CI, pre-push); no untracked files.
#
# Usage:
#   scripts/changed-files.sh [options] [--] [pathspec...]
#
#   pathspec          git pathspecs ('*.go', ':(glob)docs/**/*.md',
#                     ':(exclude)x'); none means every path
#   --trigger SPEC    a change to a path matching SPEC means "lint
#                     everything" (exit 3); repeatable. For a linter's
#                     config, lockfile or tool pin.
#   --filter LETTERS  only paths whose change is one of LETTERS (git's
#                     --diff-filter: A added, D deleted, M modified, ...);
#                     untracked files count as added
#   --full-on-delete  a deleted path (a rename's old name included) means
#                     "lint everything" (exit 3): for checks that resolve
#                     references to other files, like links
#   --include-deleted list deleted paths too (default: only paths that
#                     exist in the working tree, i.e. lintable files)
#   --base            print the resolved base commit and exit
#
# Exit:
#   0  the changed paths matching pathspec, one per line (may be empty:
#      nothing to lint)
#   3  lint everything: LINT_ALL is set, a --trigger path changed, this
#      script or scripts/lint-changed.sh changed, a path was deleted under
#      --full-on-delete, or no base could be resolved (a reason goes to
#      stderr). Never a silent skip: an unknown base lints all.
#   2  usage error
set -euo pipefail

FULL=3
SELF=(scripts/changed-files.sh scripts/lint-changed.sh)

triggers=()
include_deleted=0
full_on_delete=0
filter=""
print_base=0
while [ $# -gt 0 ]; do
    case "$1" in
        --trigger)
            [ $# -ge 2 ] || { echo "changed-files: --trigger needs a pathspec" >&2; exit 2; }
            triggers+=("$2"); shift 2 ;;
        --trigger=*) triggers+=("${1#--trigger=}"); shift ;;
        --include-deleted) include_deleted=1; shift ;;
        --full-on-delete) full_on_delete=1; shift ;;
        --filter)
            [ $# -ge 2 ] || { echo "changed-files: --filter needs letters" >&2; exit 2; }
            filter=$2; shift 2 ;;
        --base) print_base=1; shift ;;
        --) shift; break ;;
        -h|--help) sed -n '2,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p;}' "$0"; exit 0 ;;
        -*) echo "changed-files: unknown option: $1" >&2; exit 2 ;;
        *) break ;;
    esac
done

cd "$(git rev-parse --show-toplevel)"

full() {
    echo "changed-files: linting everything: $*" >&2
    exit "$FULL"
}

case "${LINT_ALL:-}" in
    ''|0|false|no) ;;
    *) full "LINT_ALL=$LINT_ALL" ;;
esac

ZERO_SHA=0000000000000000000000000000000000000000

commit_of() {
    git rev-parse --verify --quiet "$1^{commit}" 2>/dev/null
}

head_rev=HEAD
if [ -n "${LINT_HEAD:-}" ]; then
    head_rev=$(commit_of "$LINT_HEAD") || full "LINT_HEAD=$LINT_HEAD does not resolve to a commit"
fi

resolve_base() {
    local rev ref
    if [ -n "${LINT_BASE:-}" ]; then
        # A push that creates a branch reports the zero SHA as `before`.
        [ "$LINT_BASE" != "$ZERO_SHA" ] || return 1
        rev=$(commit_of "$LINT_BASE") || return 1
        git merge-base "$rev" "$head_rev" 2>/dev/null || printf '%s\n' "$rev"
        return 0
    fi
    if [ -n "${LINT_BASE_REF:-}" ]; then
        rev=$(commit_of "$LINT_BASE_REF") || return 1
        # Shallow history: no merge-base, so diff against the ref itself.
        git merge-base "$rev" "$head_rev" 2>/dev/null || printf '%s\n' "$rev"
        return 0
    fi
    # Nearest merge-base wins: a branch cut from next must not be measured
    # against main, whose older merge-base would pull every commit next
    # gained since into the changed set.
    local best="" mb
    for ref in refs/remotes/origin/next refs/remotes/origin/main refs/remotes/origin/HEAD; do
        rev=$(commit_of "$ref") || continue
        mb=$(git merge-base "$rev" "$head_rev" 2>/dev/null) || continue
        if [ -z "$best" ] || git merge-base --is-ancestor "$best" "$mb"; then
            best=$mb
        fi
    done
    [ -n "$best" ] || return 1
    printf '%s\n' "$best"
}

base=$(resolve_base) || full "no base: LINT_BASE='${LINT_BASE:-}' unresolvable and no default branch ref (fetch origin, or set LINT_BASE)"

if [ "$print_base" -eq 1 ]; then
    printf '%s\n' "$base"
    exit 0
fi

# changed [pathspec...]: every changed path (deleted ones included).
changed() {
    if [ -n "${LINT_HEAD:-}" ]; then
        git diff --name-only --no-renames "$base" "$head_rev" -- "$@"
    else
        git diff --name-only --no-renames "$base" -- "$@"
        git ls-files --others --exclude-standard -- "$@"
    fi
}

# selected [pathspec...]: changed paths that pass --filter.
selected() {
    if [ -z "$filter" ]; then
        changed "$@"
    elif [ -n "${LINT_HEAD:-}" ]; then
        git diff --name-only --no-renames --diff-filter="$filter" "$base" "$head_rev" -- "$@"
    else
        git diff --name-only --no-renames --diff-filter="$filter" "$base" -- "$@"
        case "$filter" in
            *A*) git ls-files --others --exclude-standard -- "$@" ;;
        esac
    fi
}

hit=$(changed "${SELF[@]}" | head -n 1)
[ -z "$hit" ] || full "$hit changed"
if [ "${#triggers[@]}" -gt 0 ]; then
    hit=$(changed "${triggers[@]}" | head -n 1)
    [ -z "$hit" ] || full "$hit changed"
fi

if [ "$full_on_delete" -eq 1 ]; then
    hit=$(changed | while IFS= read -r f; do [ -e "$f" ] || printf '%s\n' "$f"; done | head -n 1)
    [ -z "$hit" ] || full "$hit deleted"
fi

selected "$@" | sort -u | while IFS= read -r f; do
    if [ "$include_deleted" -eq 1 ] || [ -e "$f" ]; then
        printf '%s\n' "$f"
    fi
done
