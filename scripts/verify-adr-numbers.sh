#!/usr/bin/env bash
#
# verify-adr-numbers.sh — guardrail against duplicate ADR numbers
# claimed by concurrently-developed branches.
#
# The failure mode this exists to catch: two branches are cut from the
# same base, each author reads docs/adr/ on the default branch, sees
# NNNN as the highest number, and both claim NNNN+1. Neither branch is
# wrong on its own — the collision only exists *between* refs, so a
# checker that lints the working tree in isolation cannot see it. This
# script therefore compares the ADR numbers on the current tree against
# every other ref, and fails when one number maps to two different
# filenames.
#
# Same number + same filename across refs is fine: that is one ADR
# present on several branches. Same number + different filenames is the
# collision.
#
# Usage:
#   scripts/verify-adr-numbers.sh [<adr-dir>]
#
# Default <adr-dir>: docs/adr.
#
# CI callers must make the other refs reachable first — a default
# actions/checkout leaves only origin/<default-branch>:
#   git fetch --no-tags --depth=1 origin '+refs/heads/*:refs/remotes/origin/*'
# Without that fetch the script still runs, but only sees the refs it
# has, so it degrades to a weaker check rather than a false pass on
# something it could have seen.
set -euo pipefail

die() { printf 'verify-adr-numbers: %s\n' "$*" >&2; exit 1; }

adr_dir="${1:-docs/adr}"
[ -d "$adr_dir" ] || die "missing directory: $adr_dir"

# ADR filenames are NNNN-kebab-title.md. README.md and anything else
# without a 4-digit prefix is not an ADR and is ignored throughout.
adr_re='^[0-9]{4}-.*\.md$'

# Collect "NNNN<TAB>basename<TAB>origin" rows from the working tree and
# from every ref. `origin` is a human-readable location for the error
# message, not a git remote.
rows=$(mktemp)
trap 'rm -f "$rows"' EXIT

# Working tree — the branch under test. Listed first so its entries are
# what a reader sees at the top of any conflict report.
while IFS= read -r path; do
  base=${path##*/}
  [[ $base =~ $adr_re ]] || continue
  printf '%s\t%s\t%s\n' "${base:0:4}" "$base" "(working tree)" >>"$rows"
done < <(find "$adr_dir" -maxdepth 1 -type f -name '*.md' | sort)

# Every local and remote ref. Detached/undecorated refs are skipped by
# for-each-ref naturally; ls-tree failures on refs lacking the dir are
# non-fatal.
while IFS= read -r ref; do
  while IFS= read -r path; do
    base=${path##*/}
    [[ $base =~ $adr_re ]] || continue
    printf '%s\t%s\t%s\n' "${base:0:4}" "$base" "$ref" >>"$rows"
  done < <(git ls-tree -r --name-only "$ref" -- "$adr_dir" 2>/dev/null || true)
done < <(git for-each-ref --format='%(refname)' refs/heads refs/remotes 2>/dev/null || true)

[ -s "$rows" ] || die "no ADR files found under $adr_dir — wrong directory?"

# A number is in conflict when it maps to more than one distinct
# filename. Compare on the (number, filename) pair, deduped, so the
# same ADR appearing on twenty branches counts once.
conflicts=$(cut -f1,2 "$rows" | sort -u | cut -f1 | uniq -d)

if [ -n "$conflicts" ]; then
  echo "verify-adr-numbers: duplicate ADR numbers detected" >&2
  echo >&2
  while IFS= read -r num; do
    [ -n "$num" ] || continue
    printf 'ADR %s is claimed by %s different files:\n' \
      "$num" "$(awk -F'\t' -v n="$num" '$1==n{print $2}' "$rows" | sort -u | wc -l | tr -d ' ')" >&2
    while IFS= read -r fname; do
      [ -n "$fname" ] || continue
      printf '  %s\n' "$fname" >&2
      awk -F'\t' -v n="$num" -v f="$fname" '$1==n && $2==f{print "      on: " $3}' "$rows" \
        | sort -u >&2
    done < <(awk -F'\t' -v n="$num" '$1==n{print $2}' "$rows" | sort -u)
    echo >&2
  done <<<"$conflicts"
  echo "Renumber one of them to the next free number and update the" >&2
  echo "index table in $adr_dir/README.md in the same commit." >&2
  echo "See $adr_dir/README.md for how to claim a number." >&2
  exit 1
fi

highest=$(cut -f1 "$rows" | sort -u | tail -1)
count=$(cut -f1,2 "$rows" | sort -u | wc -l | tr -d ' ')
echo "verify-adr-numbers: ok — $count distinct ADRs across all refs, highest $highest, no duplicate numbers"
