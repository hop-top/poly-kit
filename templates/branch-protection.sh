#!/usr/bin/env bash
# branch-protection.sh — apply the default-main ruleset to a repo.
# Sourced by branch-protection-bulk.sh; also usable standalone.
#
# apply_branch_protection <owner/name>
#   stdout: "<STATUS>: <owner/name>[ (diagnostic)]"
#   STATUS: SET (created) | UPDATED (existing replaced) | FAILED
#   exit:   0 on SET/UPDATED, 1 on FAILED

BP_RULESET_NAME="hop-top-default-main"

apply_branch_protection() {
  local repo_slug="${1:-}"
  printf 'FAILED: %s (not implemented)\n' "$repo_slug"
  return 1
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  set -euo pipefail
  apply_branch_protection "$@"
fi
