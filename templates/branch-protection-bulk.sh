#!/usr/bin/env bash
# branch-protection-bulk.sh — apply the default-main ruleset to many repos.
#
# usage: branch-protection-bulk.sh [--all | <repo>...] [-- <apply flags>]
#   --all      every non-archived repo in $BP_ORG (default hop-top),
#              excluding the .github community repo
#   <repo>     shortname (under $BP_ORG) or owner/name
#   --         remaining args pass through to apply_branch_protection
# exit: 0 all SET/UPDATED, 1 any FAILED, 2 usage error

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BP_ORG="${BP_ORG:-hop-top}"
# shellcheck source=branch-protection.sh
source "$HERE/branch-protection.sh"

usage() {
  echo "usage: ${0##*/} [--all | <repo>...] [-- <apply flags>]"
}

main() {
  local all=false repos=() passthru=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help) usage; return 0 ;;
      --all) all=true ;;
      --) shift; passthru=("$@"); break ;;
      -*) echo "unknown flag: $1" >&2; usage >&2; return 2 ;;
      *) repos+=("$1") ;;
    esac
    shift
  done

  if $all; then
    local name
    while IFS= read -r name; do
      [[ -n "$name" && "$name" != ".github" ]] && repos+=("$name")
    done < <(gh repo list "$BP_ORG" --limit 1000 --json name,isArchived \
      --jq '.[] | select(.isArchived | not) | .name')
  fi

  if [[ ${#repos[@]} -eq 0 ]]; then
    usage >&2
    return 2
  fi

  local set_n=0 upd_n=0 fail_n=0 repo slug out
  for repo in "${repos[@]}"; do
    slug="$repo"
    [[ "$repo" == */* ]] || slug="$BP_ORG/$repo"
    out="$(apply_branch_protection "$slug" ${passthru[@]+"${passthru[@]}"} || true)"
    printf '%s\n' "$out"
    case "$out" in
      SET:*) set_n=$((set_n + 1)) ;;
      UPDATED:*) upd_n=$((upd_n + 1)) ;;
      *) fail_n=$((fail_n + 1)) ;;
    esac
  done

  printf '\nSET=%d UPDATED=%d FAILED=%d\n' "$set_n" "$upd_n" "$fail_n"
  [[ "$fail_n" -eq 0 ]]
}

main "$@"
